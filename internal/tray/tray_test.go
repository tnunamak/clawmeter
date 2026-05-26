//go:build tray

package tray

import (
	"context"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestIconMeterStateUsesActualExpectedAndProjectedRiskSeparately(t *testing.T) {
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
	if meter.Label != "7D" {
		t.Fatalf("meter.Label = %q, want 7D", meter.Label)
	}
	if meter.UsagePct != 50 {
		t.Fatalf("UsagePct = %.1f, want actual utilization from worst projected window", meter.UsagePct)
	}
	wantExpected := 100 / 7.0
	if absFloat(meter.ExpectedPct-wantExpected) > 0.2 {
		t.Fatalf("ExpectedPct = %.1f, want roughly %.1f", meter.ExpectedPct, wantExpected)
	}
	if meter.RiskPct < 300 {
		t.Fatalf("RiskPct = %.1f, want projected overrun severity from rate", meter.RiskPct)
	}
}

func TestProviderSeverityUsesHighestProjectedUsage(t *testing.T) {
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
	if keys[0] != "openai" {
		t.Fatalf("sortedKeys()[0] = %q, want openai because it has the highest projected usage; keys=%v", keys[0], keys)
	}
}

func TestIconTargetOverrideCyclesOnlyThroughProviderQuotaWindows(t *testing.T) {
	choices := []iconTarget{
		{Provider: "claude", Window: "5h"},
		{Provider: "claude", Window: "7d All"},
		{Provider: "openai", Window: "5h"},
		{Provider: "openai", Window: "7d"},
	}

	if got := nextIconTargetOverride(iconTarget{}, choices); got != (iconTarget{Provider: "claude", Window: "5h"}) {
		t.Fatalf("next from auto = %+v, want claude/5h", got)
	}
	if got := nextIconTargetOverride(iconTarget{Provider: "claude", Window: "5h"}, choices); got != (iconTarget{Provider: "claude", Window: "7d All"}) {
		t.Fatalf("next from claude 5h = %+v, want claude/7d All", got)
	}
	if got := nextIconTargetOverride(iconTarget{Provider: "openai", Window: "7d"}, choices); got != (iconTarget{Provider: "claude", Window: "5h"}) {
		t.Fatalf("next from final target = %+v, want claude/5h", got)
	}
	if got := nextIconTargetOverride(iconTarget{Provider: "missing"}, choices); got != (iconTarget{Provider: "claude", Window: "5h"}) {
		t.Fatalf("next from missing override = %+v, want claude/5h", got)
	}
}

func TestIconCycleMenuTitleMentionsDoubleClickAutoReset(t *testing.T) {
	displayNames := map[string]string{"claude": "Claude"}
	if got := iconCycleMenuTitle(iconTarget{}, displayNames); got != "Icon: Auto (click to cycle)" {
		t.Fatalf("auto title = %q", got)
	}
	got := iconCycleMenuTitle(iconTarget{Provider: "claude", Window: "7d All"}, displayNames)
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

func TestActiveIconTargetsIncludesEveryProviderWindow(t *testing.T) {
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h"},
				{Name: "7d"},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h"},
				{Name: "7d All"},
				{Name: "7d Sonnet"},
			},
		},
	}

	got := activeIconTargets(results)
	want := []iconTarget{
		{Provider: "claude", Window: "5h"},
		{Provider: "claude", Window: "7d All"},
		{Provider: "claude", Window: "7d Sonnet"},
		{Provider: "openai", Window: "5h"},
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
