package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	"github.com/tnunamak/clawmeter/internal/provider/openaiapi"
)

func TestOpenAIReceiptJourney(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", base)
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", base)
	}
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", base)
	}
	stores := 0
	handler := claudeweb.ExtensionHandler(nil, nil, func() (string, error) { return "synthetic-pair", nil }, func() { stores++ })
	post := func(body []byte, token, origin string) int {
		r := httptest.NewRequest("POST", "http://127.0.0.1:17344/v1/openai-api-credits", bytes.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Clawmeter-Token", token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	now := time.Now().UTC()
	yes := true
	claim := int64(3500)
	receipts := []openaicredits.Snapshot{{Pool: strings.Repeat("a", 64), Name: "Synthetic org", Credits: &openaicredits.Credits{Currency: "USD", Balance: 125, ObservedAt: now, Grants: []openaicredits.Grant{}}},
		{Pool: strings.Repeat("a", 64), Name: "Synthetic org", Benefit: &openaicredits.Benefit{Eligible: &yes, Currency: "USD", Cadence: "monthly", PeriodCredit: 7000, ClaimableCredit: &claim, ObservedAt: now, ExpiresAt: now.Add(time.Hour), LinkState: "unknown"}},
		{Pool: strings.Repeat("a", 64), Name: "Synthetic org", Link: &openaicredits.Link{State: "unlinked", ObservedAt: now}}}
	for _, r := range receipts {
		body, _ := json.Marshal(r)
		if post(body, "wrong", claudeweb.ExtensionOrigins[0]) != http.StatusUnauthorized || post(body, "synthetic-pair", "https://platform.openai.com") != http.StatusForbidden {
			t.Fatal("unpaired/page-origin request accepted")
		}
		if code := post(body, "synthetic-pair", claudeweb.ExtensionOrigins[0]); code != http.StatusNoContent {
			t.Fatalf("receipt status %d", code)
		}
	}
	if stores != 3 {
		t.Fatalf("saved notifications %d", stores)
	}
	// This is the production registry path, including automatic local discovery.
	reg := provider.NewRegistry()
	cfg := config.DefaultConfig()
	all.Register(reg, cfg)
	var p provider.Provider
	for _, candidate := range reg.GetConfigured() {
		if candidate.Name() == "openai_api" {
			p = candidate
		}
	}
	if p == nil {
		t.Fatal("saved receipt did not configure OpenAI API provider")
	}
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v := (&MultiProviderOutput{Providers: []ProviderFormatter{{Name: p.Name(), DisplayName: p.DisplayName(), Data: data}}}).buildView(now)
	if !strings.Contains(v.plainLine(0), "Claim $35.00") || !strings.Contains(v.plainLine(0), "$1.25 API credits left") {
		t.Fatal(v.plainLine(0))
	}
	if v.snap.Worst != nil {
		t.Fatal("offer or money became quota")
	}
	// Exact expiry and stale states are evaluated without another network read.
	if v.snap.Accounts[0].CreditBenefits[0].ActionURL != provider.OpenAIAPIBillingURL {
		t.Fatal("wrong recovery destination")
	}
	cache, _ := os.UserCacheDir()
	s := &openaicredits.Store{Dir: filepath.Join(cache, "clawmeter", "openai-api-credits")}
	linked := receipts[2]
	linked.Link = &openaicredits.Link{State: "linked", ObservedAt: now.Add(time.Second)}
	if err := s.Save(linked, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, err := (&openaiapi.Provider{Store: s}).FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreditBenefits[0].State(now.Add(time.Second)) != "linked" || !updated.CreditBenefits[0].ObservedAt.Equal(now) {
		t.Fatal("link failed to invalidate old offer independently")
	}
	bad := []byte(`{"pool":"` + strings.Repeat("b", 64) + `","name":"Synthetic","credits":{"currency":"USD","observed_at":"` + now.Format(time.RFC3339Nano) + `","grants":[]}}`)
	if post(bad, "synthetic-pair", claudeweb.ExtensionOrigins[0]) != http.StatusBadRequest {
		t.Fatal("missing balance became zero")
	}
}
