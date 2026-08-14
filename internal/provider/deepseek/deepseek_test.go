package deepseek

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

type resolver map[string]string

func (r resolver) ResolveSessionEnvironment(req provider.SessionEnvironmentRequest) map[string]string {
	got := map[string]string{}
	for _, name := range req.EnvNames {
		if r[name] != "" {
			got[name] = r[name]
		}
	}
	return got
}

func TestFetchUsageContractAndBalanceSemantics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user/balance" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer configured" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"12.50","granted_balance":"2","topped_up_balance":"10.5"},{"currency":"CNY","total_balance":"0","granted_balance":"0","topped_up_balance":"0"}]}`))
	}))
	defer srv.Close()
	p := New(config.ProviderConfig{APIKey: "configured"})
	p.balanceURL = srv.URL + "/user/balance"
	p.client = srv.Client()
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Balances) != 2 || data.Balances[0].Name != "usd" || data.Balances[0].DisplayName != "USD balance" || data.Balances[0].Remaining != 12.5 {
		t.Fatalf("balances = %#v", data.Balances)
	}
	if data.Balances[0].Total != 0 || data.Balances[0].Used != 0 || data.Windows != nil {
		t.Fatalf("invented usage data: %#v", data)
	}
}

func TestFetchUsageStatusSemantics(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		status               int
		expired, invalidates bool
		warning              string
		wantErr              bool
	}{{"unauthorized", 401, true, true, "", false}, {"forbidden", 403, true, true, "", false}, {"payment required", 402, false, false, "insufficient", false}, {"rate limited", 429, false, false, "", true}, {"server error", 502, false, false, "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
			defer srv.Close()
			p := New(config.ProviderConfig{APIKey: "key"})
			p.balanceURL = srv.URL
			p.client = srv.Client()
			data, err := p.FetchUsage(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
			if tc.wantErr {
				return
			}
			if data.IsExpired != tc.expired || data.InvalidatesPriorUsage != tc.invalidates || (tc.warning != "" && !strings.Contains(data.Warning, tc.warning)) {
				t.Fatalf("data = %#v", data)
			}
		})
	}
}

func TestFetchUsageWarnsWhenUnavailableAndKeepsBalances(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"is_available":false,"balance_infos":[{"currency":"USD","total_balance":"3.25"}]}`))
	}))
	defer srv.Close()
	p := New(config.ProviderConfig{APIKey: "key"})
	p.balanceURL, p.client = srv.URL, srv.Client()
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.Balances) != 1 || data.Warning == "" {
		t.Fatalf("data = %#v, err = %v", data, err)
	}
}

func TestFetchUsageRejectsMalformedAndOversizedResponses(t *testing.T) {
	for _, body := range []string{"{}", `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"bad"}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		p := New(config.ProviderConfig{APIKey: "key"})
		p.balanceURL = srv.URL
		p.client = srv.Client()
		if _, err := p.FetchUsage(context.Background()); err == nil {
			t.Fatalf("body %q accepted", body)
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", maxBody+1))) }))
	defer srv.Close()
	p := New(config.ProviderConfig{APIKey: "key"})
	p.balanceURL = srv.URL
	p.client = srv.Client()
	if _, err := p.FetchUsage(context.Background()); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestCredentialsAndSourceCapability(t *testing.T) {
	p := New(config.ProviderConfig{})
	p.SetSessionEnvironmentResolver(resolver{"DEEPSEEK_API_KEY": "env-key"})
	if !p.IsConfigured() || p.apiKey() != "env-key" {
		t.Fatal("environment credential not resolved")
	}
	capability, ok := provider.SourceCapabilityOf(p)
	if !ok {
		t.Fatal("missing source capability")
	}
	kinds := capability.SourceKinds()
	if len(kinds) != 2 || kinds[0].Kind != "native" || kinds[1].Kind != "env-name" {
		t.Fatalf("source kinds = %#v", kinds)
	}
	if _, err := capability.NewSource(config.ProviderConfig{APIKey: "config"}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "DEEPSEEK_WORK_KEY"}}); err != nil {
		t.Fatal(err)
	}
	if err := capability.ValidateSource(config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}); err == nil {
		t.Fatal("named native source accepted")
	}
}
