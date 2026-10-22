//go:build tray

package tray

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
)

// drawBalanceRows draws pools as the render path does, with the browser's
// last contact stubbed to contact.
func drawBalanceRows(t *testing.T, menu *providerMenuItems, contact time.Time, pools ...provider.UsageCreditPool) int {
	t.Helper()
	previous := lastExtensionContact
	lastExtensionContact = func() time.Time { return contact }
	t.Cleanup(func() { lastExtensionContact = previous })
	data := &provider.UsageData{Provider: provider.ClaudeAPISourceName, CreditPools: pools}
	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	return showBalanceRows(menu, data, nil)
}

func TestBalanceRowsChooseTheirStepWhenDrawn(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: provider.ClaudeAPISourceName}, false, nil, false)
	now := time.Now()
	stale := now.Add(-6 * time.Minute)
	pool := func(observed time.Time, live bool) provider.UsageCreditPool {
		return provider.UsageCreditPool{Name: "Org", Currency: "USD", Balance: 5753, ObservationSource: "extension", ObservedAt: observed, Live: live}
	}

	// Stale, with the browser reaching Clawmeter a minute ago: sign in.
	if rows := drawBalanceRows(t, menu, now.Add(-time.Minute), pool(stale, false)); rows != 1 {
		t.Fatalf("rows = %d", rows)
	}
	if !menu.balanceStates[0].enabled || menu.balanceActions[0] != "sign in to the Claude Console" ||
		!strings.HasSuffix(menu.balanceStates[0].title, "not updated since "+stale.Local().Format("15:04")+" · sign in to the Claude Console") {
		t.Fatalf("recent contact row = %+v, action %q", menu.balanceStates[0], menu.balanceActions[0])
	}

	// Fetched live but stale when drawn, and the browser is not running:
	// the row gets the browser step, not an empty line.
	if rows := drawBalanceRows(t, menu, time.Time{}, pool(stale, true)); rows != 1 {
		t.Fatalf("rows = %d", rows)
	}
	if !menu.balanceStates[0].enabled || menu.balanceActions[0] != "open your browser to update" ||
		!strings.HasSuffix(menu.balanceStates[0].title, " · open your browser to update") {
		t.Fatalf("live-at-fetch row = %+v, action %q", menu.balanceStates[0], menu.balanceActions[0])
	}

	// A plain balance row with no step stays disabled, and so does a live one.
	if rows := drawBalanceRows(t, menu, time.Time{}, pool(now, true), pool(now, true)); rows != 2 {
		t.Fatalf("rows = %d", rows)
	}
	if menu.balanceStates[0].enabled || menu.balanceStates[1].enabled || menu.balanceActions[0] != "" || menu.balanceActions[1] != "" {
		t.Fatalf("live rows clickable: %+v %+v", menu.balanceStates[0], menu.balanceStates[1])
	}
	if menu.balanceStates[2].visible || menu.balanceStates[2].enabled || menu.balanceActions[2] != "" {
		t.Fatalf("hidden row kept state: %+v, action %q", menu.balanceStates[2], menu.balanceActions[2])
	}
}

func TestBalanceRowOpensTheConsoleOnlyWhenItCarriesAStep(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: provider.ClaudeAPISourceName}, false, nil, false)
	now := time.Now()
	drawBalanceRows(t, menu, time.Time{},
		provider.UsageCreditPool{Name: "Org", Currency: "USD", Balance: 5753, ObservationSource: "extension", ObservedAt: now.Add(-6 * time.Minute), SignedOut: true},
		provider.UsageCreditPool{Name: "Plain", Currency: "USD", Balance: 100, ObservationSource: "extension", ObservedAt: now, Live: true})
	var opened []string
	for row := range 2 {
		openBalanceRow(menu, row, func(url string) { opened = append(opened, url) })
	}
	if !reflect.DeepEqual(opened, []string{claudeweb.ConsoleURL}) {
		t.Fatalf("opened = %q", opened)
	}
}
