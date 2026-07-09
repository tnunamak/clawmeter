//go:build tray

package tray

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/update"
)

func TestConfigureNotificationIdentity(t *testing.T) {
	old := beeep.AppName
	defer func() { beeep.AppName = old }()

	beeep.AppName = "DefaultAppName"
	configureNotificationIdentity()

	if beeep.AppName != "Clawmeter" {
		t.Fatalf("beeep.AppName = %q, want Clawmeter", beeep.AppName)
	}
}

func TestTrayTitleShowsUpdateIndicator(t *testing.T) {
	oldRelease := currentPendingRelease()
	defer setPendingRelease(oldRelease)

	setPendingRelease(nil)
	if got := trayTitle(); got != "Clawmeter" {
		t.Fatalf("trayTitle without update = %q", got)
	}

	setPendingRelease(&update.Release{Version: "v9.9.9"})
	if got := trayTitle(); got != "Clawmeter •" {
		t.Fatalf("trayTitle with update = %q", got)
	}
}

func TestCompactIconTooltipShowsBlockedGap(t *testing.T) {
	got := compactIconTooltip("Codex 7-Day", provider.UsageWindow{
		Name:     "7d",
		ResetsAt: time.Now().Add(3 * time.Hour),
	}, forecast.Projection{
		ProjectedPct:    124,
		WillLastToReset: false,
		RunsOutIn:       90 * time.Minute,
		RunsOutEarlyBy:  time.Hour,
	})

	for _, want := range []string{
		"Codex 7-Day",
		"Runs out in 1h30m (1h00m before reset)",
		"Resets in",
		"Est. 124% at reset",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compactIconTooltip() = %q, missing %q", got, want)
		}
	}
}

func TestCompactIconTooltipAlreadyOutShowsWaitUntilReset(t *testing.T) {
	got := compactIconTooltip("Codex 7-Day", provider.UsageWindow{
		Name:     "7d",
		ResetsAt: time.Now().Add(2 * time.Hour),
	}, forecast.Projection{
		ProjectedPct:    180,
		WillLastToReset: false,
		RunsOutEarlyBy:  2 * time.Hour,
	})

	if !strings.Contains(got, "Out now (2h00m until reset)") {
		t.Fatalf("compactIconTooltip() = %q, want already-out wait", got)
	}
}

func TestIconMeterStateUsesSoonestBlockingWindowForProviderDefault(t *testing.T) {
	now := time.Now()
	data := &provider.UsageData{
		Provider: "openai",
		Windows: []provider.UsageWindow{
			{
				Name:        "5h",
				Utilization: 90,
				ResetsAt:    now.Add(1 * time.Hour),
			},
			{
				Name:        "7d",
				Utilization: 50,
				ResetsAt:    now.Add(6 * 24 * time.Hour),
			},
		},
	}

	meter := iconMeterState(data, "")
	if !meter.ShowExpected {
		t.Fatal("meter should show expected pace for healthy usage windows")
	}
	if meter.Label != "5H" {
		t.Fatalf("meter.Label = %q, want 5H", meter.Label)
	}
	if meter.UsagePct != 90 {
		t.Fatalf("UsagePct = %.1f, want actual utilization from soonest blocking window", meter.UsagePct)
	}
	wantExpected := 80.0
	if absFloat(meter.ExpectedPct-wantExpected) > 0.5 {
		t.Fatalf("ExpectedPct = %.1f, want roughly %.1f", meter.ExpectedPct, wantExpected)
	}
	if meter.RiskPct < 110 || meter.RiskPct > 115 {
		t.Fatalf("RiskPct = %.1f, want projected overrun severity from rate", meter.RiskPct)
	}
}

func TestIconMeterStateKeepsUnavailableDataNeutral(t *testing.T) {
	meter := iconMeterState(&provider.UsageData{
		Provider: "claude",
		Error:    "usage unavailable",
	}, "")

	if meter.UsagePct != 0 || meter.ExpectedPct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want neutral provider icon without red failure gauge", meter)
	}
}

func TestIconMeterStateKeepsStaleDataNeutral(t *testing.T) {
	meter := iconMeterState(&provider.UsageData{
		Provider: "openai",
		Stale:    true,
		Warning:  "failed to fetch codex rate limits",
		Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 99, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}, "")

	if meter.UsagePct != 0 || meter.ExpectedPct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want neutral provider icon for stale data", meter)
	}
}

func TestProviderSeverityUsesRiskUrgency(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
			},
		},
	}

	keys := sortedKeys(results)
	if len(keys) == 0 {
		t.Fatal("sortedKeys() returned no providers")
	}
	if keys[0] != "claude" {
		t.Fatalf("sortedKeys()[0] = %q, want claude because it runs out sooner; keys=%v", keys[0], keys)
	}
}

func TestIconTargetOverrideCyclesOnlyThroughProviderQuotaWindows(t *testing.T) {
	choices := []iconTarget{
		{Provider: "claude", Window: "5h"},
		{Provider: "claude", Window: "7d All"},
		{Provider: "openai", Window: "5h"},
		{Provider: "openai", Window: "7d"},
	}

	if got := nextIconTargetOverride(iconTarget{}, choices, false); got != (iconTarget{Provider: "claude", Window: "5h"}) {
		t.Fatalf("next from auto without skip = %+v, want claude/5h", got)
	}
	if got := nextIconTargetOverride(iconTarget{}, choices, true); got != (iconTarget{Provider: "claude", Window: "7d All"}) {
		t.Fatalf("next from auto with skip = %+v, want claude/7d All", got)
	}
	if got := nextIconTargetOverride(iconTarget{Provider: "claude", Window: "5h"}, choices, true); got != (iconTarget{Provider: "claude", Window: "7d All"}) {
		t.Fatalf("next from claude 5h = %+v, want claude/7d All", got)
	}
	if got := nextIconTargetOverride(iconTarget{Provider: "openai", Window: "7d"}, choices, true); got != (iconTarget{Provider: "claude", Window: "5h"}) {
		t.Fatalf("next from final target = %+v, want claude/5h", got)
	}
	if got := nextIconTargetOverride(iconTarget{Provider: "missing"}, choices, true); got != (iconTarget{Provider: "claude", Window: "5h"}) {
		t.Fatalf("next from missing override = %+v, want claude/5h", got)
	}
}

func TestSelectedTrayTargetPrefersUsableQuotaOverErrorOnlyProvider(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Error:    "failed to fetch codex rate limits",
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(2 * time.Hour)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.iconAutoMode = iconAutoRisk
	s.mu.Unlock()

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "claude" || windowName != "5h" {
		t.Fatalf("selected target = %s/%s, want claude/5h", name, windowName)
	}
}

func TestIconCycleMenuTitleMentionsDoubleClickAutoReset(t *testing.T) {
	displayNames := map[string]string{"claude": "Claude"}
	if got := iconCycleMenuTitle(iconTarget{}, displayNames, iconAutoRisk); got != "Icon: Auto Risk (click to cycle)" {
		t.Fatalf("auto title = %q", got)
	}
	if got := iconCycleMenuTitle(iconTarget{}, displayNames, iconAutoProjected); got != "Icon: Auto EST (click to cycle)" {
		t.Fatalf("est auto title = %q", got)
	}
	if got := iconCycleMenuTitle(iconTarget{}, displayNames, iconAutoRunway); got != "Icon: Auto Runway (click to cycle)" {
		t.Fatalf("runway auto title = %q", got)
	}
	got := iconCycleMenuTitle(iconTarget{Provider: "claude", Window: "7d All"}, displayNames, iconAutoRisk)
	want := "Icon: Claude 7A (click for next, double-click for Auto)"
	if got != want {
		t.Fatalf("pinned title = %q, want %q", got, want)
	}
}

func TestTrayClickDispatcherSingleClickCyclesAfterWindow(t *testing.T) {
	ch := make(chan iconClickAction, 2)
	dispatcher := newTrayClickDispatcher(ch, 5*time.Millisecond)

	dispatcher.tapped()

	if got := waitIconClickAction(t, ch, 100*time.Millisecond); got != iconClickCycle {
		t.Fatalf("single click action = %v, want cycle", got)
	}
}

func TestTrayClickDispatcherDoubleClickResetsAutoWithoutCycle(t *testing.T) {
	ch := make(chan iconClickAction, 2)
	dispatcher := newTrayClickDispatcher(ch, 50*time.Millisecond)

	dispatcher.tapped()
	dispatcher.tapped()

	if got := waitIconClickAction(t, ch, 100*time.Millisecond); got != iconClickResetAuto {
		t.Fatalf("double click action = %v, want reset auto", got)
	}
	select {
	case got := <-ch:
		t.Fatalf("double click emitted extra action %v", got)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestActiveIconTargetsOrdersEveryProviderWindowByRiskUrgency(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
				{Name: "7d All", Utilization: 5, ResetsAt: now.Add(8 * time.Hour)},
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}

	got := activeIconTargets(results, iconAutoRisk)
	want := []iconTarget{
		{Provider: "claude", Window: "5h"},
		{Provider: "claude", Window: "7d Sonnet"},
		{Provider: "openai", Window: "7d"},
		{Provider: "openai", Window: "5h"},
		{Provider: "claude", Window: "7d All"},
	}
	if len(got) != len(want) {
		t.Fatalf("activeIconTargets len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeIconTargets[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestActiveIconTargetsRiskPrefersSoonerRunoutOverHigherProjectedPct(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				// Projected ~115%, but not blocked until about 3d12h from now.
				{Name: "7d", Utilization: 42.3, ResetsAt: now.Add(4*24*time.Hour + 10*time.Hour + 29*time.Minute)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				// Projected ~106%, but blocked in about 1h35m from now.
				{Name: "7d", Utilization: 99.0, ResetsAt: now.Add(10*time.Hour + 58*time.Minute)},
			},
		},
	}

	got := activeIconTargets(results, iconAutoRisk)
	want := []iconTarget{
		{Provider: "claude", Window: "7d"},
		{Provider: "openai", Window: "7d"},
	}
	if len(got) != len(want) {
		t.Fatalf("activeIconTargets len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeIconTargets[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestActiveIconTargetsESTPrefersHigherProjectedPct(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				// Projected ~115%, but not blocked until about 3d12h from now.
				{Name: "7d", Utilization: 42.3, ResetsAt: now.Add(4*24*time.Hour + 10*time.Hour + 29*time.Minute)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				// Projected ~106%, but blocked in about 1h35m from now.
				{Name: "7d", Utilization: 99.0, ResetsAt: now.Add(10*time.Hour + 58*time.Minute)},
			},
		},
	}

	got := activeIconTargets(results, iconAutoProjected)
	want := []iconTarget{
		{Provider: "openai", Window: "7d"},
		{Provider: "claude", Window: "7d"},
	}
	if len(got) != len(want) {
		t.Fatalf("activeIconTargets len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeIconTargets[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestActiveIconTargetsRunwayOrdersByMostRemainingProjectedRoom(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
				{Name: "7d All", Utilization: 5, ResetsAt: now.Add(8 * time.Hour)},
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
		"gemini": {
			Provider: "gemini",
			Error:    "usage unavailable",
		},
	}

	got := activeIconTargets(results, iconAutoRunway)
	want := []iconTarget{
		{Provider: "claude", Window: "7d All"},
		{Provider: "openai", Window: "5h"},
		{Provider: "claude", Window: "5h"},
		{Provider: "openai", Window: "7d"},
		{Provider: "claude", Window: "7d Sonnet"},
	}
	if len(got) != len(want) {
		t.Fatalf("activeIconTargets len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeIconTargets[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestActiveIconTargetsPrefersFreshWindowsOverStaleFallback(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider:  "claude",
			Stale:     true,
			Warning:   "rate limited (429)",
			FetchedAt: now.Add(-15 * time.Minute),
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 95, ResetsAt: now.Add(1 * time.Hour)},
			},
		},
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 20, ResetsAt: now.Add(5 * 24 * time.Hour)},
			},
		},
	}

	got := activeIconTargets(results, iconAutoRisk)
	want := []iconTarget{{Provider: "openai", Window: "7d"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("activeIconTargets = %+v, want only fresh target %+v", got, want)
	}
}

func TestActiveIconTargetsKeepsStaleFallbackWhenNoFreshWindows(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider:  "claude",
			Stale:     true,
			Warning:   "rate limited (429)",
			FetchedAt: now.Add(-15 * time.Minute),
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 11, ResetsAt: now.Add(4 * time.Hour)},
			},
		},
	}

	got := activeIconTargets(results, iconAutoRisk)
	want := []iconTarget{{Provider: "claude", Window: "5h"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("activeIconTargets = %+v, want stale fallback target %+v", got, want)
	}
}

func TestSelectedTrayTargetRunwayUsesMostAvailableUsableQuota(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 5, ResetsAt: now.Add(2 * 24 * time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 80, ResetsAt: now.Add(2 * time.Hour)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.iconAutoMode = iconAutoRunway
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconAutoMode = iconAutoRisk
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "openai" || windowName != "7d" {
		t.Fatalf("selected target = %s/%s, want openai/7d", name, windowName)
	}
}

func TestSelectedTrayTargetIgnoresStaleOverrideWhenFreshTargetExists(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider: "claude",
			Stale:    true,
			Warning:  "rate limited (429)",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 95, ResetsAt: now.Add(1 * time.Hour)},
			},
		},
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 20, ResetsAt: now.Add(5 * 24 * time.Hour)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "claude", Window: "5h"}
	s.iconAutoMode = iconAutoRisk
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.iconAutoMode = iconAutoRisk
		s.mu.Unlock()
	}()

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "openai" || windowName != "7d" {
		t.Fatalf("selected target = %s/%s, want openai/7d", name, windowName)
	}
}

func TestBackedOffProviderWithoutPriorWindowsStillFetches(t *testing.T) {
	gate := provider.NewFailureGate()
	_ = gate.ShouldSurfaceError("openai", false)

	toFetch, skipped := splitProvidersForRefresh(
		[]provider.Provider{trayStubProvider{name: "openai"}},
		gate,
		map[string]*provider.UsageData{},
		false,
	)

	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if got := providerNames(toFetch); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("toFetch = %v, want [openai]", got)
	}
}

func TestBackedOffProviderWithPriorWindowsUsesClone(t *testing.T) {
	gate := provider.NewFailureGate()
	_ = gate.ShouldSurfaceError("openai", true)
	prev := &provider.UsageData{
		Provider: "openai",
		Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 25, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}

	toFetch, skipped := splitProvidersForRefresh(
		[]provider.Provider{trayStubProvider{name: "openai"}},
		gate,
		map[string]*provider.UsageData{"openai": prev},
		false,
	)

	if len(toFetch) != 0 {
		t.Fatalf("toFetch = %v, want none", providerNames(toFetch))
	}
	got := skipped["openai"]
	if got == nil {
		t.Fatal("skipped[openai] is nil, want cached usage")
	}
	if got == prev {
		t.Fatal("skipped data aliases prior result, want clone")
	}
	got.Windows[0].Utilization = 99
	if prev.Windows[0].Utilization != 25 {
		t.Fatalf("mutating skipped clone changed prior result to %.0f", prev.Windows[0].Utilization)
	}
}

func TestForceRefreshIgnoresBackoff(t *testing.T) {
	gate := provider.NewFailureGate()
	_ = gate.ShouldSurfaceError("openai", true)

	toFetch, skipped := splitProvidersForRefresh(
		[]provider.Provider{trayStubProvider{name: "openai"}},
		gate,
		map[string]*provider.UsageData{
			"openai": {
				Provider: "openai",
				Windows:  []provider.UsageWindow{{Name: "7d", ResetsAt: time.Now().Add(24 * time.Hour)}},
			},
		},
		true,
	)

	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if got := providerNames(toFetch); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("toFetch = %v, want [openai]", got)
	}
}

func TestSelectedTrayTargetHonorsQuotaOverride(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "claude", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "claude" || windowName != "5h" {
		t.Fatalf("selected target = %s/%s, want claude/5h", name, windowName)
	}
}

func TestTrayTooltipDescribesCurrentAutoTarget(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()

	got := trayTooltip(results, map[string]string{"claude": "Claude", "openai": "Codex"})

	if !strings.HasPrefix(got, "Claude 7-Day Sonnet\nRuns out in ") {
		t.Fatalf("trayTooltip() = %q, want full title followed by run-out line", got)
	}
	if strings.Contains(got, "7S") || !strings.Contains(got, "Resets in") || !strings.Contains(got, "Est.") {
		t.Fatalf("trayTooltip() = %q, want run-out, reset, and estimate", got)
	}
	if strings.Contains(got, " · ") || strings.Count(got, "\n") != 3 {
		t.Fatalf("trayTooltip() = %q, want four newline-separated lines", got)
	}
}

func TestTrayTooltipDescribesPinnedTarget(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "openai", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	got := trayTooltip(results, map[string]string{"claude": "Claude", "openai": "Codex"})

	if !strings.HasPrefix(got, "Codex 5-Hour\nWon't run out") {
		t.Fatalf("trayTooltip() = %q, want full title followed by run-out state", got)
	}
	if strings.Contains(got, "5H") || !strings.Contains(got, "Resets in") || !strings.Contains(got, "Est.") {
		t.Fatalf("trayTooltip() = %q, want run-out state, reset, and estimate", got)
	}
	if strings.Contains(got, " · ") || strings.Count(got, "\n") != 3 {
		t.Fatalf("trayTooltip() = %q, want four newline-separated lines", got)
	}
}

func TestTrayTooltipIncludesResetCreditsForSelectedProvider(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
			},
			ResetCredits: &provider.UsageResetCredits{
				AvailableCount: 2,
				Credits: []provider.UsageResetCredit{
					{Status: "available", ExpiresAt: now.Add(9 * 24 * time.Hour)},
				},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "openai", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	got := trayTooltip(results, map[string]string{"openai": "Codex"})

	if !strings.Contains(got, "2 reset credits - earliest expires") {
		t.Fatalf("trayTooltip() = %q, want reset-credit expiry line", got)
	}
	if strings.Count(got, "\n") != 4 {
		t.Fatalf("trayTooltip() = %q, want five newline-separated lines with reset credits", got)
	}
}

func TestTrayTooltipDescribesStaleFallbackWithoutForecastingItAsLive(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider:  "claude",
			Stale:     true,
			Warning:   "rate limited (429)",
			FetchedAt: now.Add(-15 * time.Minute),
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 11, ResetsAt: now.Add(4 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.iconAutoMode = iconAutoRisk
	s.mu.Unlock()

	got := trayTooltip(results, map[string]string{"claude": "Claude"})

	if !strings.HasPrefix(got, "Claude: stale - showing last good data from ") {
		t.Fatalf("trayTooltip() = %q, want stale fallback summary", got)
	}
	if !strings.Contains(got, "rate limited") {
		t.Fatalf("trayTooltip() = %q, want stale reason", got)
	}
	if strings.Contains(got, "Won't run out") || strings.Contains(got, "Est.") {
		t.Fatalf("trayTooltip() = %q, should not forecast stale data like live usage", got)
	}
}

func TestHumanWindowLabelUsesReadableQuotaNames(t *testing.T) {
	tests := []struct {
		name   string
		window provider.UsageWindow
		want   string
	}{
		{
			name:   "five hour",
			window: provider.UsageWindow{Name: "5h", DisplayName: "5 hours"},
			want:   "5-Hour",
		},
		{
			name:   "seven day openai",
			window: provider.UsageWindow{Name: "7d", DisplayName: "7 days"},
			want:   "7-Day",
		},
		{
			name:   "seven day sonnet",
			window: provider.UsageWindow{Name: "7d Sonnet", DisplayName: "7 days (Sonnet)"},
			want:   "7-Day Sonnet",
		},
		{
			name:   "provider display name fallback",
			window: provider.UsageWindow{Name: "monthly", DisplayName: "Monthly Credits"},
			want:   "Monthly Credits",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanWindowLabel(tt.window); got != tt.want {
				t.Fatalf("humanWindowLabel(%+v) = %q, want %q", tt.window, got, tt.want)
			}
		})
	}
}

func TestWindowBadgeLabelUsesTwoCharacterQuotaCode(t *testing.T) {
	tests := map[string]string{
		"7d All":     "7A",
		"5h":         "5H",
		"monthly":    "MO",
		"???":        "--",
		"7d Sonnet":  "7S",
		"daily-soft": "DA",
	}
	for input, want := range tests {
		if got := windowBadgeLabel(input); got != want {
			t.Fatalf("windowBadgeLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExpectedUsagePctClampsToResetWindow(t *testing.T) {
	if got := expectedUsagePct(time.Now().Add(forecast.SevenDayWindow+time.Hour), forecast.SevenDayWindow); got != 0 {
		t.Fatalf("future-before-window expected usage = %.1f, want 0", got)
	}
	if got := expectedUsagePct(time.Now().Add(-time.Hour), forecast.SevenDayWindow); got != 100 {
		t.Fatalf("past-reset expected usage = %.1f, want 100", got)
	}
}

func waitIconClickAction(t *testing.T, ch <-chan iconClickAction, timeout time.Duration) iconClickAction {
	t.Helper()
	select {
	case action := <-ch:
		return action
	case <-time.After(timeout):
		t.Fatal("timed out waiting for icon click action")
	}
	return iconClickCycle
}

func absFloat(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}

type trayStubProvider struct {
	name string
}

func (p trayStubProvider) Name() string {
	return p.name
}

func (p trayStubProvider) DisplayName() string {
	return p.name
}

func (p trayStubProvider) Description() string {
	return ""
}

func (p trayStubProvider) DashboardURL() string {
	return ""
}

func (p trayStubProvider) IsConfigured() bool {
	return true
}

func (p trayStubProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return &provider.UsageData{Provider: p.name}, nil
}
