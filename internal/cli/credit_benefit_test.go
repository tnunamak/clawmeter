package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestCreditBenefitSurfacesShareClaimAndFreshness(t *testing.T) {
	at := matrixNow
	claim, period := int64(3500), int64(7000)
	data := &provider.UsageData{Provider: "openai_api", FetchedAt: at, CreditBenefits: []provider.UsageCreditBenefit{{
		Eligibility: "eligible", LinkState: "unlinked", Currency: "USD", Claimable: &claim, PeriodCredit: &period,
		Cadence: "monthly", ObservedAt: at, ExpiresAt: at.Add(time.Hour),
	}}}
	m := &MultiProviderOutput{Providers: []ProviderFormatter{{Name: "openai_api", DisplayName: "OpenAI API", Data: data}}}
	v := m.buildView(at)
	for name, output := range map[string]string{"plain": v.plainLine(0), "table": strings.Join(v.colorTable(), "\n"), "agent": v.agentLine()} {
		if !strings.Contains(output, "Claim $35.00") || strings.Contains(output, "$35.00 left") || strings.Contains(output, "100%") {
			t.Fatalf("%s hid the action or presented an offer as funded quota: %s", name, output)
		}
	}
	if v.snap.Worst != nil || len(v.snap.Accounts[0].Windows) != 0 {
		t.Fatal("credit offer entered quota risk selection")
	}
	b, err := v.jsonDocument(nil, at)
	if err != nil || strings.Count(string(b), `"credit_benefits"`) != 2 || !strings.Contains(string(b), `"state": "claimable"`) {
		t.Fatalf("raw/evaluated JSON lost the offer: %s, %v", b, err)
	}
	// An unrelated, fresh balance poll must not refresh eligibility.
	data.FetchedAt = at.Add(6 * time.Minute)
	later := m.buildView(data.FetchedAt)
	if benefit := later.snap.Accounts[0].CreditBenefits[0]; benefit.Claimable != nil || benefit.State != "stale" {
		t.Fatalf("new poll revived old offer: %+v", benefit)
	}
	if strings.Contains(later.plainLine(0), "Claim $") {
		t.Fatal("human output kept a stale monetary claim")
	}
}

func TestAPIBalanceSurfacesHaveNoQuotaOrInventedForecast(t *testing.T) {
	at := matrixNow
	data := &provider.UsageData{Provider: "openai_api", FetchedAt: at, APICreditBalances: []provider.UsageAPICreditBalance{{Name: "Synthetic org", Currency: "USD", Balance: 125, ObservedAt: at, Grants: []provider.APICreditExpiry{{Remaining: 125, ExpiresAt: at.Add(time.Hour)}}}}}
	m := &MultiProviderOutput{Providers: []ProviderFormatter{{Name: "openai_api", DisplayName: "OpenAI API", Data: data}}}
	v := m.buildView(at)
	for name, output := range map[string]string{"plain": v.plainLine(0), "table": strings.Join(v.colorTable(), "\n"), "agent": v.agentLine()} {
		if !strings.Contains(output, "$1.25") || strings.Contains(output, "no spend") || strings.Contains(output, "unused at expiry") || strings.Contains(output, "100%") {
			t.Fatalf("%s: %s", name, output)
		}
	}
	if v.snap.Worst != nil || v.snap.Accounts[0].APICreditBalances[0].Available == nil {
		t.Fatal("funded balance became quota or disappeared")
	}
	later := m.buildView(at.Add(6 * time.Minute))
	if later.snap.Accounts[0].APICreditBalances[0].Available != nil || !strings.Contains(later.plainLine(0), "last checked balance") {
		t.Fatal("stale balance advertised usable money")
	}
	expired := m.buildView(at.Add(time.Hour))
	if expired.snap.Accounts[0].APICreditBalances[0].Available != nil || !strings.Contains(expired.plainLine(0), "expired since last check") {
		t.Fatal("expired balance remained usable")
	}
}
