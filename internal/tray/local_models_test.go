//go:build tray

package tray

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestOpenCodeFreeLocalModelsShowObservedActivity(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "opencode_free"}, false, nil, false)
	// Plasma creates a submenu only when children-display is present on the
	// row's first export. A hidden child establishes that before data arrives.
	if len(menu.localModelItems) != 1 || menu.localModelStates[0].visible {
		t.Fatal("OpenCode submenu must be declared before its first refresh")
	}
	models := make([]provider.LocalModelUsage, 39)
	for i := range models {
		models[i] = provider.LocalModelUsage{Model: fmt.Sprintf("provider/model-%02d", i), Day: "2026-10-28"}
	}
	used := int64(3)
	models[12].AssistantMessages = &used
	models[35].LimitHitAt = time.Now()
	acct := &view.Account{LocalModels: models}

	if rows := showBalanceRows(menu, acct, nil, time.Now()); rows != len(models) {
		t.Fatalf("reported rows = %d, want %d", rows, len(models))
	}
	if !menu.localModelsState.visible || !menu.localModelsState.enabled || menu.localModelsState.title != "Local activity (2 models)" {
		t.Fatalf("submenu parent = %+v", menu.localModelsState)
	}
	if len(menu.localModelItems) != 2 {
		t.Fatalf("submenu items = %d, want only 2 models with observations", len(menu.localModelItems))
	}
	want := []provider.LocalModelUsage{models[12], models[35]}
	for i, item := range menu.localModelStates {
		if !item.visible || item.enabled || item.title != want[i].Summary() {
			t.Fatalf("submenu row %d = %+v", i, item)
		}
	}
	showLocalModelRows(menu, []provider.LocalModelUsage{{Model: "unused-free"}})
	if menu.localModelsState.enabled || menu.localModelsState.title != "Local activity unavailable · quota unknown" {
		t.Fatalf("unused catalog appeared as capacity: %+v", menu.localModelsState)
	}
	zero := int64(0)
	showLocalModelRows(menu, []provider.LocalModelUsage{{Model: "unused-free", AssistantMessages: &zero}})
	if menu.localModelsState.enabled || menu.localModelsState.title != "No local activity · quota unknown" {
		t.Fatal("known zero and unknown activity conflated")
	}
	for _, item := range menu.localModelStates {
		if item.visible {
			t.Fatal("old activity survived an empty observation")
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

func TestOpenCodeLocalActivityDoesNotHideMissingQuota(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "opencode_free"}, false, nil, false)
	now := time.Now()
	s.mu.Lock()
	oldResults := s.lastResults
	s.lastResults = map[string]*provider.UsageData{"opencode_free": {
		Provider: "opencode_free", FetchedAt: now,
		LocalModels: []provider.LocalModelUsage{{Model: "example-free", Day: now.UTC().Format("2006-01-02")}},
	}}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.lastResults = oldResults
		s.mu.Unlock()
	}()
	updateUI(nil, nil, map[string]*providerMenuItems{"opencode_free": menu},
		systray.AddMenuItem("reauth", ""), newIconPicker(), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	if !menu.statusState.visible || !strings.Contains(menu.statusState.title, "Remaining quota not reported") {
		t.Fatalf("missing quota was hidden: %+v", menu.statusState)
	}
}
