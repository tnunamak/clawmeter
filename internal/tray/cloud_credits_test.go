//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestCloudCreditRowKeepsAPIRecoveryOnItsOwnRow(t *testing.T) {
	defer systray.ResetMenu()
	now := time.Now()
	used, limit := 7.125, 250.0
	data := &provider.UsageData{Provider: "claude", FetchedAt: now,
		CreditGrants: []provider.UsageCreditGrant{{Name: "cloud_sessions", DisplayName: "Cloud session credits", Scope: "cloud_sessions", Currency: "USD", Used: &used, Limit: &limit, ExpiresAt: now.Add(time.Hour)}},
		CreditPools:  []provider.UsageCreditPool{{Name: "API credits", Currency: "USD", Balance: 1000, ObservationSource: "extension", ObservedAt: now.Add(-time.Hour), SignedOut: true}},
	}
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"claude": data}}, now)
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "claude"}, false, nil, false)
	menu.extensionReady = true
	if got := showBalanceRows(menu, &snap.Accounts[0], nil, now); got != 2 {
		t.Fatalf("money rows = %d", got)
	}
	if menu.balanceStates[0].title != snap.Accounts[0].CreditGrants[0].Summary() || menu.balanceStates[0].enabled || strings.Contains(menu.balanceStates[0].title, "reset") {
		t.Fatalf("cloud credit rendered as a quota/action: %+v", menu.balanceStates[0])
	}
	if !menu.balanceStates[1].enabled || !strings.HasPrefix(menu.balanceStates[1].title, "API credits:") {
		t.Fatalf("API recovery moved onto wrong row: %+v", menu.balanceStates[1])
	}
	var opened []string
	for row := range 2 {
		openBalanceRow(menu, row, func(url string) { opened = append(opened, url) })
	}
	if len(opened) != 1 || opened[0] != claudeweb.ConsoleURL {
		t.Fatalf("money row dispatch = %q", opened)
	}
}

func TestCloudCreditExpirySchedulesAnOfflineRepaint(t *testing.T) {
	now := time.Now()
	expiry := now.Add(time.Minute)
	results := map[string]*provider.UsageData{"claude": {CreditGrants: []provider.UsageCreditGrant{{ExpiresAt: expiry}}}}
	if got := nextAPICreditDeadline(results, now); !got.Equal(expiry) {
		t.Fatalf("next repaint = %v, want %v", got, expiry)
	}
	if got := nextAPICreditDeadline(results, expiry); !got.IsZero() {
		t.Fatalf("past expiry reschedules itself: %v", got)
	}
}
