//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestCreditBenefitRowOpensOpenAIAndHonorsVisibility(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "openai_api"}, false, nil, false)
	at := time.Now()
	claim, period := int64(3500), int64(7000)
	data := &provider.UsageData{Provider: "openai_api", FetchedAt: at, CreditBenefits: []provider.UsageCreditBenefit{{
		Eligibility: "eligible", LinkState: "unlinked", Currency: "USD", Claimable: &claim, PeriodCredit: &period,
		Cadence: "monthly", ObservedAt: at, ExpiresAt: at.Add(time.Hour),
	}}}
	draw := func(now time.Time) {
		snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"api": data}}, now)
		showBalanceRows(menu, accountNamed(snap, "api"), nil, now)
	}
	draw(at)
	if !menu.balanceStates[0].enabled || !strings.Contains(menu.balanceStates[0].title, "Claim $35.00") {
		t.Fatalf("claim row is not actionable: %+v", menu.balanceStates[0])
	}
	var opened []string
	openBalanceRow(menu, 0, func(url string) { opened = append(opened, url) })
	if len(opened) != 1 || opened[0] != provider.OpenAIAPIBillingURL {
		t.Fatalf("claim opened wrong provider: %v", opened)
	}
	hideProviderWindows(menu)
	openBalanceRow(menu, 0, func(string) { t.Fatal("queued hidden claim opened a page") })
	draw(at.Add(6 * time.Minute))
	if strings.Contains(menu.balanceStates[0].title, "Claim $") || !menu.balanceStates[0].enabled {
		t.Fatal("stale offer should carry a check step without a claim amount")
	}
	data.CreditBenefits[0].LinkState = "linked"
	draw(at)
	openBalanceRow(menu, 0, func(string) { t.Fatal("linked benefit invited another claim") })
}

func TestCreditBenefitAndBalanceScheduleOfflineFreshnessAndExpiry(t *testing.T) {
	now := time.Now()
	expiry := now.Add(time.Minute)
	data := &provider.UsageData{CreditBenefits: []provider.UsageCreditBenefit{{ObservedAt: now, ExpiresAt: expiry}}, APICreditBalances: []provider.UsageAPICreditBalance{{ObservedAt: now}}}
	if at := nextAPICreditDeadline(map[string]*provider.UsageData{"api": data}, now); !at.Equal(expiry) {
		t.Fatalf("repaint %v want expiry %v", at, expiry)
	}
	data.CreditBenefits = nil
	if at := nextAPICreditDeadline(map[string]*provider.UsageData{"api": data}, now); !at.Equal(now.Add(5 * time.Minute)) {
		t.Fatal("missing balance freshness deadline")
	}
}
