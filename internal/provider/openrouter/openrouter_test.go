package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func providerServer(t *testing.T, handler http.HandlerFunc, management bool) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := &Provider{cfg: config.ProviderConfig{APIKey: "standard-secret"}, client: server.Client(), creditsURL: server.URL + "/credits", keyURL: server.URL + "/key"}
	if management {
		p.managementKey = "management-secret"
	}
	return p
}

func TestStandardKeyUsesKeyOnlyAndComputesCapFromRemaining(t *testing.T) {
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/credits" {
			t.Errorf("standard key must not call credits")
		}
		if r.Header.Get("Authorization") != "Bearer standard-secret" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":7,"usage":99,"limit_reset":"monthly"}}`))
	}, false)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Balances) != 0 || len(data.Windows) != 1 || data.Windows[0].Utilization != 30 || data.Windows[0].ResetPolicy != "monthly" || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("data = %#v", data)
	}
}

func TestManagementKeyOnlyUsesCredits(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key" {
			t.Errorf("management-only must not call key")
			return
		}
		if r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":{"total_credits":100.5,"total_usage":25.75}}`))
	}, true)
	p.cfg.APIKey = ""
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Balances) != 1 || data.Balances[0].Remaining != 74.75 || len(data.Windows) != 0 {
		t.Fatalf("data = %#v", data)
	}
}

func TestBothKeysFetchIndependentSurfaces(t *testing.T) {
	seen := map[string]string{}
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Get("Authorization")
		if r.URL.Path == "/key" {
			_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":0,"usage":0}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"total_credits":1,"total_usage":0}}`))
	}, true)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seen["/key"] != "Bearer standard-secret" || seen["/credits"] != "Bearer management-secret" || len(data.Balances) != 1 || data.Windows[0].Utilization != 100 {
		t.Fatalf("seen=%v data=%#v", seen, data)
	}
}

func TestCredentialSurfaceStatusesAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		management bool
		key        string
		path       string
		status     int
	}{
		{"standard-401", false, "standard-secret", "/key", 401}, {"standard-403", false, "standard-secret", "/key", 403},
		{"standard-429", false, "standard-secret", "/key", 429}, {"management-401", true, "", "/credits", 401}, {"management-403", true, "", "/credits", 403},
		{"management-429", true, "", "/credits", 429}, {"management-500", true, "", "/credits", 500}, {"management-503", true, "", "/credits", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENROUTER_API_KEY", "")
			p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte("standard-secret management-secret"))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":5,"usage":0}}`))
			}, tc.management)
			p.cfg.APIKey = tc.key
			data, err := p.FetchUsage(context.Background())
			if tc.status == 401 || tc.status == 403 {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("data=%#v err=%v", data, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestMalformedCreditsAndMissingFields(t *testing.T) {
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":{"total_credits":1}}`)) }, true)
	p.cfg.APIKey = ""
	_, err := p.FetchUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing credits fields") {
		t.Fatalf("err=%v", err)
	}
}
