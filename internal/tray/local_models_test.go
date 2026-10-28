//go:build tray

package tray

import (
	"fmt"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestOpenCodeFreeLocalModelsUseUnboundedOwnedSubmenu(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "opencode_free"}, false, nil, false)
	models := make([]provider.LocalModelUsage, 39)
	for i := range models {
		models[i] = provider.LocalModelUsage{Model: fmt.Sprintf("provider/model-%02d", i), Day: "2026-10-28"}
	}
	acct := &view.Account{LocalModels: models}

	if rows := showBalanceRows(menu, acct, nil, time.Now()); rows != len(models) {
		t.Fatalf("reported rows = %d, want %d", rows, len(models))
	}
	if !menu.localModelsState.visible || !menu.localModelsState.enabled || menu.localModelsState.title != "Free models (39) · local observations" {
		t.Fatalf("submenu parent = %+v", menu.localModelsState)
	}
	if len(menu.localModelItems) != 39 {
		t.Fatalf("submenu items = %d, want all 39 catalog entries", len(menu.localModelItems))
	}
	for i, item := range menu.localModelStates {
		if !item.visible || item.enabled || item.title != models[i].Summary() {
			t.Fatalf("submenu row %d = %+v", i, item)
		}
	}

	hideProviderWindows(menu)
	if menu.localModelsState.visible {
		t.Fatal("submenu parent remained visible after provider rows were hidden")
	}
	for i, item := range menu.localModelStates {
		if item.visible {
			t.Fatalf("submenu row %d remained visible after provider rows were hidden", i)
		}
	}
}
