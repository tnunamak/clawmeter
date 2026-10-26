//go:build tray

package tray

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

// productionView builds the snapshot the way updateUI does: the same
// visibility rule, the same inputs builder, and the same run-out record and
// pin, then the view. The matrix in internal/cli builds the same snapshot
// through the CLI's adapter; these tests check the tray's own path.
func productionView(results map[string]*provider.UsageData, menus map[string]*providerMenuItems, now time.Time) view.Snapshot {
	shown := trayShown(results, results, menus, nil)
	return view.Build(trayViewInputs(shown, menus, now, time.Time{}, currentIconPin()), now)
}

// matrixNowForTray is the clock the CLI matrix renders at, so the strings here
// are the matrix's strings.
var matrixNowForTray = time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)

func TestRenderPathExpiredWithNoWindowsDrawsTheWarning(t *testing.T) {
	now := matrixNowForTray
	results := map[string]*provider.UsageData{"claude": {Provider: "claude", IsExpired: true, FetchedAt: now.Add(-time.Hour)}}
	menus := testMenus("claude")
	snap := productionView(results, menus, now)

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()
	if got := Tooltip(snap, results); got != "Claude: token expired" {
		t.Fatalf("tooltip = %q", got)
	}
	provider, meter := IconState(snap)
	if provider != "claude" || meter.UsagePct != 100 || meter.ExpectedPct != 100 || meter.RiskPct != 100 {
		t.Fatalf("icon = %s %+v; want the expired warning meter for claude", provider, meter)
	}
	if rows := MenuRows(snap, maxWindowItems); len(rows["claude"]) != 0 {
		t.Fatalf("menu rows = %q; an expired account shows its problem, not rows", rows["claude"])
	}
}

func TestRenderPathPinnedStaleWindowKeepsLastKnownMeter(t *testing.T) {
	now := matrixNowForTray
	results := map[string]*provider.UsageData{
		"claude": {Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
			Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: now.Add(time.Hour)}}},
		"codex": {Provider: "openai", FetchedAt: now,
			Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 30, ResetsAt: now.Add(3 * time.Hour)}}},
	}
	menus := testMenus("claude", "codex")

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "claude", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	snap := productionView(results, menus, now)
	const wantTooltip = "Claude 5-Hour\nstale - showing last good data from Oct 22, 2026 11:00 AM (rate limited)\nCurrent 100%\nRun-out unknown\nResets in 1h00m\nforecast stale"
	if got := Tooltip(snap, results); got != wantTooltip {
		t.Fatalf("tooltip = %q\nwant      %q", got, wantTooltip)
	}
	if _, meter := IconState(snap); meter.UsagePct != 100 || meter.RiskPct != 0 || !meter.ShowExpected || meter.Label != "5H" {
		t.Fatalf("icon for a pinned stale window = %+v; retain usage and identity without inventing a forecast", meter)
	}
	rows := MenuRows(snap, maxWindowItems)
	if got := strings.Join(rows["claude"], " | "); !strings.Contains(got, "forecast stale") || strings.Contains(got, "out now") {
		t.Fatalf("stale menu row = %q", got)
	}
}

// The run-out record reaches the tooltip and the menu row through the tray's
// own inputs, so a window the tray saw run out says so on both.
func TestRenderPathOutSinceComesFromTheTraysRecord(t *testing.T) {
	windowExhaustion.load(filepath.Join(t.TempDir(), "exhaustion.json"), testMaxGap)
	t.Cleanup(func() { windowExhaustion.load("", 0) })

	now := time.Now()
	reset := now.Add(3 * time.Hour)
	before, after := now.Add(-10*time.Minute), now.Add(-5*time.Minute)
	full := func(at time.Time) map[string]*provider.UsageData {
		return map[string]*provider.UsageData{"claude": {FetchedAt: at, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: reset}}}}
	}
	identities := map[string]string{"claude": testAccount}
	windowExhaustion.observeFetched(map[string]*provider.UsageData{
		"claude": {FetchedAt: before, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 80, ResetsAt: reset}}},
	}, nil, identities, before, testMaxGap)
	windowExhaustion.observeFetched(full(after), nil, identities, after, testMaxGap)

	menus := testMenus("claude")
	snap := productionView(full(after), menus, now)
	if got := Tooltip(snap, full(after)); !strings.Contains(got, "\nOut since "+runOutSince(after, now)) {
		t.Fatalf("tooltip = %q; want the tray's out-since time", got)
	}
	if rows := MenuRows(snap, maxWindowItems); !strings.Contains(strings.Join(rows["claude"], " | "), "out since "+runOutSince(after, now)) {
		t.Fatalf("menu rows = %q; want the tray's out-since time", rows["claude"])
	}
}

func TestRenderPathMoreCountsWindowsTheMenuDoesNotDraw(t *testing.T) {
	now := matrixNowForTray
	windows := make([]provider.UsageWindow, 0, 10)
	for i := 0; i < 9; i++ {
		windows = append(windows, provider.UsageWindow{Name: "w" + string(rune('a'+i)), Length: 5 * time.Hour, Utilization: 40, ResetsAt: now.Add(3 * time.Hour)})
	}
	results := map[string]*provider.UsageData{"claude": {Provider: "claude", FetchedAt: now, Windows: windows}}
	snap := productionView(results, testMenus("claude"), now)
	if n := MenuMore(snap, maxWindowItems)["claude"]; n != 1 {
		t.Fatalf("more = %d; the ninth window has no row, so it is one more", n)
	}
	if got := len(MenuRows(snap, maxWindowItems)["claude"]); got != maxWindowItems {
		t.Fatalf("rows = %d, want %d", got, maxWindowItems)
	}
}
