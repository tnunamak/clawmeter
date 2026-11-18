package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// The shapes below are the live responses observed on 2026-10-29 and
// 2026-11-17, with synthetic IDs and amounts.
const (
	discoveryTwoOrgs = `{"destinations":[{"destination_type":"api_org","destination_account_id":"org-personal","display_name":"Personal"},` +
		`{"destination_type":"api_org","destination_account_id":"org-team","display_name":"Team"},` +
		`{"destination_type":"workspace","destination_account_id":"ws-other","display_name":"Other"},` +
		`{"destination_type":"api_org","destination_account_id":"bad/id","display_name":"Bad"}]}`
	eligibleOffer   = `{"eligible":true,"ineligibility_reason":null,"offer":{"currency":"USD","cadence":"monthly","period_credit_amount_usd":"200.00","expires_at":"2026-12-10T08:00:00Z","claimable_credit_amount_usd":"123.455"}}`
	ineligibleOffer = `{"eligible":false,"ineligibility_reason":"program_unavailable","offer":null}`
)

func benefitServer(t *testing.T, routes map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("benefit reader sent %s %s", r.Method, r.URL.Path)
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-access" || r.Header.Get("ChatGPT-Account-ID") != "acct-1" ||
			r.Header.Get("OAI-Product-Sku") != "CODEX" || r.Header.Get("Originator") != "Codex Desktop" {
			t.Errorf("missing Codex headers on %s", r.URL.Path)
		}
		path := strings.TrimPrefix(r.URL.Path, productLinksPath)
		switch path {
		case "/discovery":
			if r.URL.Query().Get("account_id") != "acct-1" || r.URL.Query().Get("destination_type") != "api_org" {
				t.Errorf("discovery query = %s", r.URL.RawQuery)
			}
		case "/accounts/acct-1" + apiCreditBenefitPath:
			if r.URL.Query().Get("destination_type") != "api_org" {
				t.Errorf("benefit query = %s", r.URL.RawQuery)
			}
		}
		body, ok := routes[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(body, "<html") {
			w.WriteHeader(http.StatusForbidden)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	oldURL, oldClient := productLinksURL, apiCreditHTTPClient
	productLinksURL, apiCreditHTTPClient = server.URL+productLinksPath, server.Client()
	t.Cleanup(func() { productLinksURL, apiCreditHTTPClient = oldURL, oldClient })
}

func codexLogin(t *testing.T, auth string) *Provider {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if auth != "" {
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(auth), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return New(config.ProviderConfig{})
}

const chatGPTLogin = `{"tokens":{"access_token":"test-access","account_id":"acct-1"}}`

func TestReadAPICreditBenefitsBindsTheOfferToEachDiscoveredOrganization(t *testing.T) {
	benefitServer(t, map[string]string{
		"/discovery": discoveryTwoOrgs,
		"/accounts/acct-1" + apiCreditBenefitPath: eligibleOffer,
		"/organizations/org-personal":             `{"link":null,"can_link":true}`,
		"/organizations/org-team":                 `{"link":{"id":"link-1"},"can_link":false}`,
	})
	p := codexLogin(t, chatGPTLogin)
	if !p.HasChatGPTAuth() {
		t.Fatal("ChatGPT login not recognized")
	}
	before := time.Now()
	got, err := p.ReadAPICreditBenefits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("benefits = %+v, want the two API organizations", got)
	}
	personal, team := got[0], got[1]
	if personal.Pool != openaicredits.PoolID("org-personal") || personal.Name != "Personal" || team.Pool != openaicredits.PoolID("org-team") {
		t.Fatalf("pools/names = %+v", got)
	}
	if strings.Contains(personal.Pool, "org-") {
		t.Fatal("raw organization ID left the reader")
	}
	if personal.Eligibility != "eligible" || *personal.PeriodCredit != 20000 || *personal.Claimable != 12346 ||
		personal.Cadence != "monthly" || !personal.ExpiresAt.Equal(time.Date(2026, 12, 10, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("offer = %+v", personal)
	}
	if personal.LinkState != "unlinked" || team.LinkState != "linked" {
		t.Fatalf("link states = %s, %s", personal.LinkState, team.LinkState)
	}
	if personal.ObservedAt.Before(before) || personal.LinkObservedAt.Before(personal.ObservedAt) {
		t.Fatalf("observation times = %v, %v", personal.ObservedAt, personal.LinkObservedAt)
	}
	*personal.Claimable = 0
	if *team.Claimable != 12346 {
		t.Fatal("organizations share one offer amount")
	}
}

func TestReadAPICreditBenefitsKeepsIneligibilityWithoutAmounts(t *testing.T) {
	benefitServer(t, map[string]string{
		"/discovery": discoveryTwoOrgs,
		"/accounts/acct-1" + apiCreditBenefitPath: ineligibleOffer,
		"/organizations/org-personal":             `{"link":null,"can_link":true}`,
		"/organizations/org-team":                 `{"link":null,"can_link":false}`,
	})
	got, err := codexLogin(t, chatGPTLogin).ReadAPICreditBenefits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Eligibility != "ineligible" || got[0].PeriodCredit != nil || got[0].Claimable != nil {
		t.Fatalf("benefits = %+v", got)
	}
	if got[1].LinkState != "unknown" {
		t.Fatalf("a null link that cannot be created is %s, want unknown", got[1].LinkState)
	}
	if got[0].Summary(got[0].ObservedAt) != "" {
		t.Fatal("ineligible account was shown a claim")
	}
}

func TestReadAPICreditBenefitsRejectsIncompleteOrBlockedReads(t *testing.T) {
	for name, routes := range map[string]map[string]string{
		"cloudflare challenge": {"/discovery": "<html>challenge</html>"},
		"offer missing amount": {"/discovery": discoveryTwoOrgs, "/accounts/acct-1" + apiCreditBenefitPath: strings.Replace(eligibleOffer, `"200.00"`, `""`, 1)},
		"claim above period":   {"/discovery": discoveryTwoOrgs, "/accounts/acct-1" + apiCreditBenefitPath: strings.Replace(eligibleOffer, `"123.455"`, `"200.01"`, 1)},
		"float amount":         {"/discovery": discoveryTwoOrgs, "/accounts/acct-1" + apiCreditBenefitPath: strings.Replace(eligibleOffer, `"200.00"`, `200`, 1)},
		"no eligibility":       {"/discovery": discoveryTwoOrgs, "/accounts/acct-1" + apiCreditBenefitPath: `{"offer":null}`},
		"unknown cadence":      {"/discovery": discoveryTwoOrgs, "/accounts/acct-1" + apiCreditBenefitPath: strings.Replace(eligibleOffer, "monthly", "weekly", 1)},
		"link read fails":      {"/discovery": discoveryTwoOrgs, "/accounts/acct-1" + apiCreditBenefitPath: eligibleOffer},
	} {
		t.Run(name, func(t *testing.T) {
			benefitServer(t, routes)
			got, err := codexLogin(t, chatGPTLogin).ReadAPICreditBenefits(context.Background())
			if err == nil || got != nil {
				t.Fatalf("got %+v, %v; want an error and no partial reading", got, err)
			}
		})
	}
}

func TestReadAPICreditBenefitsNeedsAChatGPTLogin(t *testing.T) {
	for name, auth := range map[string]string{
		"no auth file": "",
		"API key only": `{"OPENAI_API_KEY":"sk-test"}`,
		"no account":   `{"tokens":{"access_token":"test-access"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := codexLogin(t, auth)
			if p.HasChatGPTAuth() {
				t.Fatal("login without a ChatGPT account counted as one")
			}
			if _, err := p.ReadAPICreditBenefits(context.Background()); !errors.Is(err, errNoChatGPTAuth) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestUSDCentsRoundsHalfAwayFromZeroWithoutFloats(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "200": 20000, "200.00": 20000, "0.004": 0, "0.005": 1, "123.455": 12346,
		"99.999": 10000, "1000000000": 100000000000, "0000000000200.00": 20000} {
		if got, ok := usdCents(in); !ok || got != want {
			t.Errorf("usdCents(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "-1", "1e3", "1.", ".5", " 1", "1000000000.01", "1,000"} {
		if _, ok := usdCents(in); ok {
			t.Errorf("usdCents(%q) accepted", in)
		}
	}
}

var _ interface {
	ReadAPICreditBenefits(context.Context) ([]provider.UsageCreditBenefit, error)
} = (*Provider)(nil)

func TestTransportErrorsDoNotCarryAccountOrOrganizationIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // every request now fails in transport
	oldURL, oldClient := productLinksURL, apiCreditHTTPClient
	productLinksURL, apiCreditHTTPClient = server.URL+productLinksPath, server.Client()
	t.Cleanup(func() { productLinksURL, apiCreditHTTPClient = oldURL, oldClient })
	_, err := codexLogin(t, chatGPTLogin).ReadAPICreditBenefits(context.Background())
	if err == nil || strings.Contains(err.Error(), "acct-1") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("err = %v", err)
	}
}
