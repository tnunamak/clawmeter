//go:build tray

package tray

import (
	"reflect"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
)

func TestBalanceRowsOpenTheConsoleOnlyWhenTheyCarryAnAction(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: provider.ClaudeAPISourceName}, false, nil, false)
	now := time.Now()
	data := &provider.UsageData{Provider: provider.ClaudeAPISourceName, CreditPools: []provider.UsageCreditPool{
		{Name: "Org", Currency: "USD", Balance: 5753, ObservationSource: "extension", ObservedAt: now.Add(-6 * time.Minute), Action: "sign in to the Claude Console"},
		{Name: "Plain", Currency: "USD", Balance: 100, ObservationSource: "extension", ObservedAt: now, Live: true},
	}}
	trayRenderMu.Lock()
	rows := showBalanceRows(menu, data, nil)
	trayRenderMu.Unlock()
	if rows != 2 || !menu.balanceStates[0].enabled || menu.balanceStates[1].enabled || menu.balanceStates[2].visible || menu.balanceStates[2].enabled {
		t.Fatalf("rows = %d, states = %+v", rows, menu.balanceStates[:3])
	}
	if !reflect.DeepEqual(menu.balanceActions[:2], []string{"sign in to the Claude Console", ""}) {
		t.Fatalf("actions = %q", menu.balanceActions[:2])
	}
	var opened []string
	for row := range 2 {
		openBalanceRow(menu, row, func(url string) { opened = append(opened, url) })
	}
	if !reflect.DeepEqual(opened, []string{claudeweb.ConsoleURL}) {
		t.Fatalf("opened = %q", opened)
	}

	// The fix clears: the row goes back to a plain, disabled balance.
	data.CreditPools[0].Action = ""
	trayRenderMu.Lock()
	showBalanceRows(menu, data, nil)
	trayRenderMu.Unlock()
	if menu.balanceStates[0].enabled || menu.balanceActions[0] != "" {
		t.Fatalf("cleared row still clickable: %+v, %q", menu.balanceStates[0], menu.balanceActions[0])
	}
}
