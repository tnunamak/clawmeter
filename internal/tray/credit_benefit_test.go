//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
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

func TestOpenAIOfflineTimerRepaintsWithoutProviderPoll(t *testing.T) {
	for _, scenario := range []string{"freshness", "link freshness", "offer expiry", "grant expiry"} {
		t.Run(scenario, func(t *testing.T) {
			defer systray.ResetMenu()
			now := time.Now()
			observed, linkAt, expiry, grantExpiry := now.Add(-time.Minute), now.Add(-time.Minute), now.Add(time.Hour), now.Add(time.Hour)
			drawAt := now.Add(-2 * time.Second)
			switch scenario {
			case "freshness":
				observed = now.Add(-6 * time.Minute)
				linkAt = observed
				drawAt = observed.Add(4 * time.Minute)
			case "link freshness":
				linkAt = now.Add(-6 * time.Minute)
				drawAt = now.Add(-2 * time.Minute)
			case "offer expiry":
				expiry = now.Add(-time.Second)
			case "grant expiry":
				grantExpiry = now.Add(-time.Second)
			}
			if scenario == "link freshness" {
				observed = drawAt.Add(-time.Minute)
			}
			claim, period := int64(3500), int64(7000)
			data := &provider.UsageData{Provider: "openai_api", FetchedAt: observed,
				APICreditBalances: []provider.UsageAPICreditBalance{{Name: "Synthetic org", Currency: "USD", Balance: 125, ObservedAt: observed, Grants: []provider.APICreditExpiry{{Remaining: 125, ExpiresAt: grantExpiry}}}},
				CreditBenefits:    []provider.UsageCreditBenefit{{Eligibility: "eligible", LinkState: "unlinked", LinkObservedAt: linkAt, Currency: "USD", Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ObservedAt: observed, ExpiresAt: expiry}}}
			results := map[string]*provider.UsageData{"openai_api": data}
			menu := createProviderMenuItems(sourceMenuTestProvider{name: "openai_api"}, false, nil, false)
			snap := view.Build(view.Inputs{Results: results}, drawAt)
			showBalanceRows(menu, accountNamed(snap, "openai_api"), nil, drawAt)
			if !strings.Contains(menu.balanceStates[1].title, "Claim $35.00") {
				t.Fatal("fixture did not begin with an actionable offer")
			}
			trayRenderMu.Lock()
			oldCfg := cfg
			cfg = config.DefaultConfig()
			s.mu.Lock()
			oldResults := s.lastResults
			s.lastResults = results
			s.mu.Unlock()
			scheduleAPICreditRows(results, map[string]*providerMenuItems{"openai_api": menu}, drawAt)
			generation := apiCreditsTimerGeneration
			trayRenderMu.Unlock()
			defer func() {
				trayRenderMu.Lock()
				defer trayRenderMu.Unlock()
				apiCreditsTimerGeneration++
				if apiCreditsTimer != nil {
					apiCreditsTimer.Stop()
				}
				cfg = oldCfg
				s.mu.Lock()
				s.lastResults = oldResults
				s.mu.Unlock()
			}()
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-deadline.C:
					t.Fatal("offline callback did not complete")
				case <-tick.C:
					trayRenderMu.Lock()
					completed := apiCreditsTimerGeneration > generation
					balance, benefit := menu.balanceStates[0].title, menu.balanceStates[1].title
					trayRenderMu.Unlock()
					if !completed {
						continue
					}
					if scenario == "grant expiry" {
						if !strings.Contains(balance, "expired since last check") || !strings.Contains(benefit, "Claim $35.00") {
							t.Fatalf("independent expiry repaint: balance=%s benefit=%s", balance, benefit)
						}
					} else if strings.Contains(benefit, "Claim $") || !strings.Contains(benefit, "Check API credit benefit") {
						t.Fatalf("offline claim survived %s: %s", scenario, benefit)
					}
					if scenario == "freshness" && !strings.Contains(balance, "last checked balance") {
						t.Fatalf("stale balance survived offline callback: %s", balance)
					}
					return
				}
			}
		})
	}
}
