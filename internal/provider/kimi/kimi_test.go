package kimi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestRefreshIsInMemoryAndPreservesOmittedRefreshToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeKimiCredentials(t, home, Credentials{AccessToken: "old", RefreshToken: "old-refresh", ExpiresAt: float64(time.Now().Add(-time.Hour).Unix())})

	var gotForm string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			body, _ := io.ReadAll(r.Body)
			gotForm = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new","expires_in":3600}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer new" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"usage":{"limit":10,"used":0,"reset_in":0}}`))
	}))
	defer server.Close()

	p := New(config.ProviderConfig{})
	p.tokenURL, p.usageURL = server.URL+"/token", server.URL+"/usage"
	p.httpClient = server.Client()
	if _, err := p.FetchUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotForm, "refresh_token=old-refresh") {
		t.Fatalf("refresh form %q does not contain encoded refresh token", gotForm)
	}
	data, err := os.ReadFile(filepath.Join(home, ".kimi", "credentials", "kimi-code.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "new") {
		t.Fatalf("refresh wrote new access token to credential file: %s", data)
	}
}

func TestRefreshRejectsPartialPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"expires_in":3600}`))
	}))
	defer server.Close()
	p := New(config.ProviderConfig{})
	p.tokenURL, p.httpClient = server.URL, server.Client()
	if _, err := p.refreshAccessToken(context.Background(), "secret"); err == nil {
		t.Fatal("partial refresh payload should fail")
	}
}

func TestParseResetTimeLeavesUnknownAndMalformedValuesUnknown(t *testing.T) {
	p := New(config.ProviderConfig{})
	p.now = func() time.Time { return time.Unix(1000, 0) }
	if got := p.parseResetTime("", 0, 0); !got.IsZero() {
		t.Fatalf("missing reset = %v, want unknown", got)
	}
	if got := p.parseResetTime("not-a-time", 0, 0); !got.IsZero() {
		t.Fatalf("malformed reset = %v, want unknown", got)
	}
	if got, want := p.parseResetTime("", 30, 0), time.Unix(1030, 0); !got.Equal(want) {
		t.Fatalf("relative reset = %v, want %v", got, want)
	}
}

func TestConfigAndEnvironmentTokensKeepExpiryUnknown(t *testing.T) {
	t.Setenv("KIMI_ACCESS_TOKEN", "")
	configured := New(config.ProviderConfig{OAuthToken: "configured"})
	creds, err := configured.readCredentials()
	if err != nil || creds.ExpiresAt != 0 || creds.IsExpired() || creds.ExpiresWithin(365*24*time.Hour) {
		t.Fatalf("configured token credentials = %+v, err = %v; want unknown non-expired expiry", creds, err)
	}

	t.Setenv("KIMI_ACCESS_TOKEN", "environment")
	environment := New(config.ProviderConfig{})
	creds, err = environment.readCredentials()
	if err != nil || creds.ExpiresAt != 0 || creds.IsExpired() || creds.ExpiresWithin(365*24*time.Hour) {
		t.Fatalf("environment token credentials = %+v, err = %v; want unknown non-expired expiry", creds, err)
	}
}

func TestTransformUsagePreservesZeroAndPartialWindows(t *testing.T) {
	p := New(config.ProviderConfig{})
	p.now = func() time.Time { return time.Unix(1000, 0) }
	data := p.transformUsage(&usageResponse{Usage: &usageSummary{Limit: jsonIntPtr(10), Used: jsonIntPtr(0)}, Limits: []limitItem{{Detail: limitDetail{Limit: jsonIntPtr(4), Remaining: jsonIntPtr(4), ResetAt: "bad"}}}})
	if len(data.Windows) != 2 || data.Windows[0].Used != 0 || data.Windows[1].Used != 0 {
		t.Fatalf("partial/zero payload = %#v", data.Windows)
	}
	if !data.Windows[0].ResetsAt.IsZero() || !data.Windows[1].ResetsAt.IsZero() {
		t.Fatalf("unknown resets must remain zero: %#v", data.Windows)
	}
}

func TestTransformUsageUsesNeutralMainNameAndProviderTitle(t *testing.T) {
	p := New(config.ProviderConfig{})
	data := p.transformUsage(&usageResponse{Usage: &usageSummary{
		Name:  "Weekly",
		Title: "Kimi Code weekly quota",
		Limit: jsonIntPtr(10),
		Used:  jsonIntPtr(0),
	}})
	if len(data.Windows) != 1 {
		t.Fatalf("window count = %d, want 1", len(data.Windows))
	}
	if got := data.Windows[0].Name; got != "usage" {
		t.Fatalf("main usage name = %q, want neutral usage", got)
	}
	if got := data.Windows[0].DisplayName; got != "Kimi Code weekly quota" {
		t.Fatalf("display name = %q, want provider title", got)
	}
}

func TestTransformUsageRejectsLimitWithoutUsageEvidence(t *testing.T) {
	data := New(config.ProviderConfig{}).transformUsage(&usageResponse{Usage: &usageSummary{Limit: jsonIntPtr(10)}})
	if len(data.Windows) != 0 {
		t.Fatalf("windows = %#v, want missing usage omitted", data.Windows)
	}
}

func jsonIntPtr(value int) *jsonInt {
	v := jsonInt(value)
	return &v
}

func TestFetchUsageHandlesAuthRateLimitAndServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"token-should-not-leak"}`))
			}))
			defer server.Close()
			p := New(config.ProviderConfig{OAuthToken: "secret-token"})
			p.usageURL, p.httpClient = server.URL, server.Client()
			data, err := p.FetchUsage(context.Background())
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				if err != nil || data == nil || data.Error == "" {
					t.Fatalf("FetchUsage() = %#v, %v; want structured auth error", data, err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "token-should-not-leak") {
				t.Fatalf("FetchUsage() error = %v; want safe transport error", err)
			}
		})
	}
}

func TestFetchUsageRejectsMalformedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":`))
	}))
	defer server.Close()
	p := New(config.ProviderConfig{OAuthToken: "secret-token"})
	p.usageURL, p.httpClient = server.URL, server.Client()
	if _, err := p.FetchUsage(context.Background()); err == nil {
		t.Fatal("malformed usage payload should fail")
	}
}

func TestIsConfigured_RequiresUsableKimiCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := New(config.ProviderConfig{})
	if p.IsConfigured() {
		t.Fatal("missing Kimi credentials should not be configured")
	}

	writeKimiCredentials(t, home, Credentials{
		AccessToken: "expired",
		ExpiresAt:   float64(time.Now().Add(-time.Hour).Unix()),
	})
	if p.IsConfigured() {
		t.Fatal("expired Kimi access token without refresh token should not be configured")
	}

	writeKimiCredentials(t, home, Credentials{
		AccessToken:  "expired",
		RefreshToken: "refresh",
		ExpiresAt:    float64(time.Now().Add(-time.Hour).Unix()),
	})
	if !p.IsConfigured() {
		t.Fatal("expired Kimi access token with refresh token should be configured")
	}
}

func writeKimiCredentials(t *testing.T, home string, creds Credentials) {
	t.Helper()
	dir := filepath.Join(home, ".kimi", "credentials")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"access_token":"` + creds.AccessToken + `","refresh_token":"` + creds.RefreshToken + `","expires_at":` + formatFloat(creds.ExpiresAt) + `}`)
	if err := os.WriteFile(filepath.Join(dir, "kimi-code.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
