package synthetic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestParseQuotasPreservesKnownSlotsAndZero(t *testing.T) {
	input := json.RawMessage(`{"rollingFiveHourLimit":{"limit":100,"used":0},"weeklyTokenLimit":{"limit":200,"used":50,"resetAt":"not-a-timestamp"},"search":{"hourly":{"limit":10,"used":5,"resetAt":"2026-07-16T18:00:00Z"}}}`)
	data, err := NewParseProvider().parseQuotas(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 3 {
		t.Fatalf("got %d windows, want 3", len(data.Windows))
	}
	if data.Windows[0].Utilization != 0 {
		t.Fatalf("zero lane utilization = %v", data.Windows[0].Utilization)
	}
	if !data.Windows[0].ResetsAt.IsZero() || !data.Windows[1].ResetsAt.IsZero() {
		t.Fatal("missing or malformed reset was invented")
	}
	if data.Windows[0].Name != rollingFiveHourName || data.Windows[1].Name != weeklyTokenName || data.Windows[2].Name != searchHourlyName || data.Windows[2].DisplayName != "Search hourly" || data.Windows[2].Utilization != 50 {
		t.Fatalf("search lane = %+v", data.Windows[2])
	}
}

func TestParseQuotasUsesDocumentedSubscriptionAndRejectsUnknownShape(t *testing.T) {
	data, err := NewParseProvider().parseQuotas(json.RawMessage(`{"subscription":{"limit":135,"requests":0}}`))
	if err != nil || len(data.Windows) != 1 {
		t.Fatalf("subscription: data=%+v err=%v", data, err)
	}
	if data.Windows[0].Utilization != 0 || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("subscription window = %+v", data.Windows[0])
	}
	unknown, err := NewParseProvider().parseQuotas(json.RawMessage(`{"balance":100,"total":200}`))
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Error == "" || len(unknown.Windows) != 0 {
		t.Fatalf("unknown shape = %+v", unknown)
	}
}

func TestParseQuotasMalformedJSON(t *testing.T) {
	if _, err := NewParseProvider().parseQuotas(json.RawMessage(`{"subscription":`)); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestParseQuotasConflictingValuesPreferExplicitPercent(t *testing.T) {
	data, err := NewParseProvider().parseQuotas(json.RawMessage(`{"subscription":{"percentUsed":25,"limit":100,"used":90}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := data.Windows[0].Utilization; got != 25 {
		t.Fatalf("utilization = %v, want 25", got)
	}
}

func TestFetchUsageTransportStatusesAndRedaction(t *testing.T) {
	secret := "synthetic-secret-token"
	for _, tc := range []struct {
		status  int
		expired bool
	}{
		{http.StatusUnauthorized, true}, {http.StatusForbidden, true},
		{http.StatusTooManyRequests, false}, {http.StatusInternalServerError, false},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "Bearer "+secret {
					t.Fatalf("request = %s %s auth=%q accept=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("provider body containing " + secret))
			}))
			defer srv.Close()
			p := New(config.ProviderConfig{APIKey: secret})
			p.endpoint, p.httpClient = srv.URL, srv.Client()
			data, err := p.FetchUsage(t.Context())
			if tc.expired {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("auth result = data=%+v err=%v", data, err)
				}
			} else if err == nil || data != nil {
				t.Fatalf("error result = data=%+v err=%v", data, err)
			}
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Fatal("secret leaked in error")
			}
		})
	}
}

func TestFetchUsageRejectsMalformedAndOversizedBodies(t *testing.T) {
	for _, body := range []string{"{\"subscription\":", strings.Repeat("x", maxBody+1)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		p := New(config.ProviderConfig{APIKey: "secret"})
		p.endpoint, p.httpClient = srv.URL, srv.Client()
		if _, err := p.FetchUsage(t.Context()); err == nil {
			t.Fatalf("body length %d accepted", len(body))
		}
		srv.Close()
	}
}

// NewParseProvider keeps parser tests independent of credentials and transport.
func NewParseProvider() *Provider { return New(config.ProviderConfig{}) }
