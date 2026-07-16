package zai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestSafeEndpoint(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"api.z.ai", "https://api.z.ai", true},
		{" https://open.bigmodel.cn/base ", "https://open.bigmodel.cn/base", true},
		{"http://api.z.ai", "", false}, {"https://user:pass@api.z.ai", "", false},
		{"https://api.z.ai/?token=secret", "", false}, {"", "", false},
	}
	for _, tt := range tests {
		got, ok := safeEndpoint(tt.raw)
		if got != tt.want || ok != tt.ok {
			t.Errorf("safeEndpoint(%q) = %q, %v; want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGetQuotaURLRegionsAndInvalidOverride(t *testing.T) {
	t.Setenv("Z_AI_QUOTA_URL", "")
	t.Setenv("Z_AI_API_HOST", "")
	t.Setenv("Z_AI_REGION", "")
	if got := New(config.ProviderConfig{}).getQuotaURL(); got != defaultBaseURL+quotaPath {
		t.Fatal(got)
	}
	t.Setenv("Z_AI_REGION", "cn")
	if got := New(config.ProviderConfig{}).getQuotaURL(); got != "https://open.bigmodel.cn"+quotaPath {
		t.Fatal(got)
	}
	t.Setenv("Z_AI_QUOTA_URL", "http://127.0.0.1:1")
	if got := New(config.ProviderConfig{}).getQuotaURL(); got != "" {
		t.Fatal("unsafe override accepted")
	}
}

func TestTransformLimitsIsTypedAndConservative(t *testing.T) {
	reset := int64(1770000000000)
	zero := int64(0)
	limit := int64(100)
	data := New(config.ProviderConfig{}).transformLimits(&apiResponse{Data: apiData{Limits: []apiLimit{
		{Type: "TOKENS_LIMIT", Unit: 3, Number: 5, Usage: &limit, CurrentValue: ptr(20), Remaining: ptr(60), NextResetTime: &reset},
		{Type: "TOKENS_LIMIT", Unit: 6, Number: 1, Usage: &zero, CurrentValue: &zero, Percentage: intPtr(0)},
		{Type: "UNKNOWN", Usage: &limit},
	}}})
	if len(data.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(data.Windows))
	}
	if data.Windows[0].Name != "tokens_5h" || data.Windows[0].Used != 40 || data.Windows[0].Utilization != 40 {
		t.Fatalf("unexpected 5h window: %+v", data.Windows[0])
	}
	if !data.Windows[1].ResetsAt.IsZero() || data.Windows[1].Name != "tokens_weekly" {
		t.Fatalf("missing reset was fabricated: %+v", data.Windows[1])
	}
}

func TestTransformLimitsClampsNegativeValues(t *testing.T) {
	data := New(config.ProviderConfig{}).transformLimits(&apiResponse{Data: apiData{Limits: []apiLimit{
		{Type: "TOKENS_LIMIT", Usage: ptr(-10), CurrentValue: ptr(-5), Remaining: ptr(-20), Percentage: intPtr(-4)},
	}}})
	if data.Windows[0].Used != 0 || data.Windows[0].Limit != 0 || data.Windows[0].Utilization != 0 {
		t.Fatalf("negative values were not clamped: %+v", data.Windows[0])
	}
}

func TestTransformLimitsRequiresExplicitUsageEvidence(t *testing.T) {
	data := New(config.ProviderConfig{}).transformLimits(&apiResponse{Data: apiData{Limits: []apiLimit{
		{Type: "TOKENS_LIMIT", Unit: 3, Number: 5},
		{Type: "TOKENS_LIMIT", Unit: 6, Number: 1, Percentage: intPtr(0)},
	}}})
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 {
		t.Fatalf("windows = %#v, want only explicit zero usage", data.Windows)
	}
}

func ptr(value int64) *int64 { return &value }
func intPtr(value int) *int  { return &value }

func TestFetchUsageBoundsErrorsAndRedactsKey(t *testing.T) {
	secret := "zai-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("missing bearer auth")
		}
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("provider response without " + secret))
	}))
	defer server.Close()
	t.Setenv("Z_AI_QUOTA_URL", "https://api.z.ai")
	p := New(config.ProviderConfig{APIKey: secret})
	p.client = server.Client()
	p.client.Transport = rewriteTransport{target: server.URL, base: http.DefaultTransport}
	_, err := p.FetchUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchUsageHandlesAuthAndServerStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			t.Setenv("Z_AI_QUOTA_URL", "https://api.z.ai")
			p := New(config.ProviderConfig{APIKey: "test-key"})
			p.client = server.Client()
			p.client.Transport = rewriteTransport{target: server.URL, base: http.DefaultTransport}
			data, err := p.FetchUsage(context.Background())
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("auth result = %#v, %v", data, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), strconv.Itoa(status)) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(t.target, "http://")
	return t.base.RoundTrip(clone)
}

func TestTransformPreservesKnownReset(t *testing.T) {
	reset := time.Now().Add(time.Hour).UnixMilli()
	data := New(config.ProviderConfig{}).transformLimits(&apiResponse{Data: apiData{Limits: []apiLimit{{Type: "TIME_LIMIT", Unit: 6, Number: 1, Percentage: intPtr(0), NextResetTime: &reset}}}})
	if len(data.Windows) != 1 || !data.Windows[0].ResetsAt.Equal(time.UnixMilli(reset)) {
		t.Fatalf("reset = %+v", data.Windows)
	}
}
