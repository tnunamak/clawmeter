//go:build tray

package tray

import (
	"sort"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

// viewOf is the snapshot of results as the tray renders it. Each source is
// labelled by its capitalized key, and the user's pin is read as the tray reads it.
func viewOf(results map[string]*provider.UsageData, now time.Time) view.Snapshot {
	names := make(map[string]string, len(results))
	for key := range results {
		names[key] = providerDisplayName(key, nil)
	}
	return viewNamed(results, names, now)
}

// viewNamed is viewOf with the display names given. Only the sources named
// are shown, as the tray shows only its menus' sources.
func viewNamed(results map[string]*provider.UsageData, names map[string]string, now time.Time) view.Snapshot {
	in := view.Inputs{
		Results:            make(map[string]*provider.UsageData, len(names)),
		DisplayNames:       make(map[string]string, len(names)),
		SourceLabels:       make(map[string]string, len(names)),
		Families:           make(map[string]string, len(names)),
		FamilySourceCounts: make(map[string]int, len(names)),
		Pin:                currentIconPin(),
	}
	for key, name := range names {
		in.Results[key] = results[key]
		in.DisplayNames[key] = name
		in.Families[key] = key
		in.FamilySourceCounts[key] = 1
	}
	return view.Build(in, now)
}

// trayViewOf is the snapshot the production render builds from menus and
// results: the same visibility rule, inputs and run-out record as updateUI.
func trayViewOf(results map[string]*provider.UsageData, menus map[string]*providerMenuItems, now time.Time) view.Snapshot {
	shown := trayShown(results, results, menus, nil)
	return view.Build(trayViewInputs(shown, menus, now, time.Time{}, currentIconPin()), now)
}

// testMenus gives each source key a menu whose provider reports the key as its name.
func testMenus(keys ...string) map[string]*providerMenuItems {
	menus := make(map[string]*providerMenuItems, len(keys))
	for i, key := range keys {
		menus[key] = &providerMenuItems{
			provider:    sourceMenuTestProvider{name: key, id: "default"},
			windowItems: make([]*systray.MenuItem, maxWindowItems),
			order:       i,
		}
	}
	return menus
}

// riskOrderOf lists every window of the snapshot from most to least at risk,
// by the Risk rank the view gives each window.
func riskOrderOf(snap view.Snapshot) []iconTarget {
	type ranked struct {
		target iconTarget
		risk   int
	}
	var all []ranked
	for _, acct := range snap.Accounts {
		for _, w := range acct.Windows {
			all = append(all, ranked{iconTarget{Provider: acct.Key, Window: w.Name}, w.Risk})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].risk < all[j].risk })
	out := make([]iconTarget, 0, len(all))
	for _, r := range all {
		out = append(out, r.target)
	}
	return out
}

// viewWindowOf is the snapshot window of a single fresh reading.
func viewWindowOf(t *testing.T, window provider.UsageWindow) view.Window {
	t.Helper()
	snap := viewOf(map[string]*provider.UsageData{"a": {Windows: []provider.UsageWindow{window}}}, time.Now())
	if len(snap.Accounts) != 1 || len(snap.Accounts[0].Windows) != 1 {
		t.Fatalf("snapshot = %+v; want one window", snap.Accounts)
	}
	return snap.Accounts[0].Windows[0]
}

// A provider turned off in config is never shown, even the Token Plan, which
// is otherwise shown whenever its console login is detected.
func TestTrayShownSkipsDisabledProvider(t *testing.T) {
	menus := testMenus(tokenPlanProviderName)
	setups := map[string]provider.SetupStatus{tokenPlanProviderName: {State: provider.SetupNeedsAuth}}
	if _, ok := trayShown(nil, nil, menus, setups)[tokenPlanProviderName]; !ok {
		t.Fatal("detected Token Plan is not shown")
	}
	menus[tokenPlanProviderName].disabled = true
	if _, ok := trayShown(nil, nil, menus, setups)[tokenPlanProviderName]; ok {
		t.Fatal("disabled Token Plan is shown")
	}
}
