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
	want := []provider.LocalModelUsage{models[12], models[35]}

	if rows := showBalanceRows(menu, acct, nil, time.Now()); rows != len(want) {
		t.Fatalf("reported rows = %d, want %d observed models", rows, len(want))
	}
	if !menu.localModelsState.visible || !menu.localModelsState.enabled || menu.localModelsState.title != "Local activity (2 models)" {
		t.Fatalf("submenu parent = %+v", menu.localModelsState)
	}
	if len(menu.localModelItems) != 2 {
		t.Fatalf("submenu items = %d, want only 2 models with observations", len(menu.localModelItems))
	}
	for i, item := range menu.localModelStates {
		if !item.visible || item.enabled || item.title != want[i].Summary() {
			t.Fatalf("submenu row %d = %+v", i, item)
		}
	}
	// Unused catalog entries, unknown or zero, get no group at all.
	if rows := showLocalModelRows(menu, []provider.LocalModelUsage{{Model: "unused-free"}}); rows != 0 || menu.localModelsState.visible {
		t.Fatalf("unused catalog showed a group: rows=%d %+v", rows, menu.localModelsState)
	}
	zero := int64(0)
	if rows := showLocalModelRows(menu, []provider.LocalModelUsage{{Model: "unused-free", AssistantMessages: &zero}}); rows != 0 || menu.localModelsState.visible {
		t.Fatalf("zero-count catalog showed a group: rows=%d %+v", rows, menu.localModelsState)
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
	messages := int64(3)
	s.mu.Lock()
	oldResults := s.lastResults
	s.lastResults = map[string]*provider.UsageData{"opencode_free": {
		Provider: "opencode_free", FetchedAt: now,
		LocalModels: []provider.LocalModelUsage{{Model: "example-free", Day: now.UTC().Format("2006-01-02"), AssistantMessages: &messages}},
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

func TestOpenCodePrimaryStatusNamesTheObservedModelLimit(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "opencode_free"}, false, nil, false)
	now := time.Now()
	event := now.Add(-time.Hour)
	s.mu.Lock()
	old := s.lastResults
	s.lastResults = map[string]*provider.UsageData{"opencode_free": {Provider: "opencode_free", FetchedAt: now, LocalModels: []provider.LocalModelUsage{{Model: "model-a", Label: "Model A", Day: now.UTC().Format("2006-01-02"), LimitHitAt: event, RetryAt: now.Add(-time.Minute)}}}}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.lastResults = old; s.mu.Unlock() }()
	updateUI(nil, nil, map[string]*providerMenuItems{"opencode_free": menu}, systray.AddMenuItem("reauth", ""), newIconPicker(), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	for _, want := range []string{"Latest recorded model limit: Model A", event.UTC().Format("Jan 2 15:04 UTC"), "remaining quota and reset unknown"} {
		if !strings.Contains(menu.statusState.title, want) {
			t.Fatalf("primary tray status lacks %q: %+v", want, menu.statusState)
		}
	}
}
