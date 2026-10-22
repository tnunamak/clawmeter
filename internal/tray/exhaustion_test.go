//go:build tray

package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
)

const testMaxGap = 10 * time.Minute

// windowAt is a five-hour window at the given utilization, resetting at the given time.
func windowAt(util float64, resetsAt time.Time) provider.UsageWindow {
	return provider.UsageWindow{Name: "5h", Utilization: util, ResetsAt: resetsAt}
}

func TestExhaustionReportsFirstFullAfterBelow(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(80, reset), base)
	l.observe("claude", windowAt(100, reset), base.Add(5*time.Minute))

	got, ok := l.ranOutAt("claude", windowAt(100, reset), testMaxGap)
	if !ok || !got.Equal(base.Add(5*time.Minute)) {
		t.Fatalf("ranOutAt = %v, %t; want first full reading at %v", got, ok, base.Add(5*time.Minute))
	}
}

func TestExhaustionHasNoTimeWhenFirstSeenFull(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(100, reset), base)
	l.observe("claude", windowAt(100, reset), base.Add(5*time.Minute))

	if got, ok := l.ranOutAt("claude", windowAt(100, reset), testMaxGap); ok {
		t.Fatalf("ranOutAt = %v; want no time when the tray first saw the window full", got)
	}
}

func TestExhaustionHasNoTimeAcrossAGapLargerThanMaxGap(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(80, reset), base)
	l.observe("claude", windowAt(100, reset), base.Add(30*time.Minute))

	if got, ok := l.ranOutAt("claude", windowAt(100, reset), testMaxGap); ok {
		t.Fatalf("ranOutAt = %v; want no time across a gap of 30m with maxGap 10m", got)
	}
}

func TestExhaustionNewResetPeriodClearsTheRecord(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(80, reset), base)
	l.observe("claude", windowAt(100, reset), base.Add(5*time.Minute))

	next := reset.Add(5 * time.Hour)
	if got, ok := l.ranOutAt("claude", windowAt(100, reset), testMaxGap); !ok {
		t.Fatalf("ranOutAt for the old period = %v, %t; want the old transition", got, ok)
	}
	// The period rolls over to a new reset, and the window is full again on
	// its first reading. That is not a transition the tray saw.
	l.observe("claude", windowAt(100, next), base.Add(5*time.Hour))
	if got, ok := l.ranOutAt("claude", windowAt(100, next), testMaxGap); ok {
		t.Fatalf("ranOutAt in the new period = %v; want none", got)
	}
	if got, ok := l.ranOutAt("claude", windowAt(100, reset), testMaxGap); ok {
		t.Fatalf("ranOutAt for the old reset = %v; want none once the period moved on", got)
	}
}

func TestExhaustionResetJitterKeepsTheRecord(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(80, reset.Add(-3*time.Second)), base)
	l.observe("claude", windowAt(100, reset.Add(2*time.Second)), base.Add(5*time.Minute))

	got, ok := l.ranOutAt("claude", windowAt(100, reset.Add(-1*time.Second)), testMaxGap)
	if !ok || !got.Equal(base.Add(5*time.Minute)) {
		t.Fatalf("ranOutAt with jittered resets = %v, %t; want the transition kept", got, ok)
	}
}

func TestExhaustionDropToBelowStartsAnotherTransition(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(100, reset), base)
	l.observe("claude", windowAt(60, reset), base.Add(5*time.Minute))
	l.observe("claude", windowAt(100, reset), base.Add(10*time.Minute))

	got, ok := l.ranOutAt("claude", windowAt(100, reset), testMaxGap)
	if !ok || !got.Equal(base.Add(10*time.Minute)) {
		t.Fatalf("ranOutAt after dropping below = %v, %t; want the second transition", got, ok)
	}
}

func TestExhaustionNoTimeWhileWindowIsNotFull(t *testing.T) {
	var l exhaustionLog
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(80, reset), base)
	l.observe("claude", windowAt(100, reset), base.Add(5*time.Minute))

	if got, ok := l.ranOutAt("claude", windowAt(90, reset), testMaxGap); ok {
		t.Fatalf("ranOutAt for a window now at 90%% = %v; want none", got)
	}
}

func TestExhaustionIgnoresWindowsWithoutAReset(t *testing.T) {
	var l exhaustionLog
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	l.observe("claude", windowAt(100, time.Time{}), base)
	if len(l.Windows) != 0 {
		t.Fatalf("observed a window with no reset: %+v", l.Windows)
	}
	if _, ok := l.ranOutAt("claude", windowAt(100, time.Time{}), testMaxGap); ok {
		t.Fatal("ranOutAt returned a time for a window with no reset")
	}
}

func TestObserveFetchedOnlyCountsFreshReadings(t *testing.T) {
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	fresh := func(util float64, at time.Time) *provider.UsageData {
		return &provider.UsageData{FetchedAt: at, Windows: []provider.UsageWindow{windowAt(util, reset)}}
	}

	var tr exhaustionTracker
	tr.observeFetched(map[string]*provider.UsageData{
		"fresh":   fresh(80, base),
		"stale":   fresh(80, base),
		"errored": fresh(80, base),
		"expired": fresh(80, base),
		"cached":  fresh(80, base),
	}, nil, base, testMaxGap)
	tr.observeFetched(map[string]*provider.UsageData{
		"fresh":   fresh(100, base.Add(5*time.Minute)),
		"stale":   {Stale: true, FetchedAt: base.Add(5 * time.Minute), Windows: []provider.UsageWindow{windowAt(100, reset)}},
		"errored": {Error: "boom", FetchedAt: base.Add(5 * time.Minute), Windows: []provider.UsageWindow{windowAt(100, reset)}},
		"expired": {IsExpired: true, FetchedAt: base.Add(5 * time.Minute), Windows: []provider.UsageWindow{windowAt(100, reset)}},
		"cached":  fresh(100, base.Add(5*time.Minute)),
		"nothing": nil,
	}, map[string]*provider.UsageData{"cached": fresh(100, base.Add(5*time.Minute))}, base.Add(5*time.Minute), testMaxGap)

	at := base.Add(5 * time.Minute)
	if got := tr.ranOutAt("fresh", windowAt(100, reset)); !got.Equal(at) {
		t.Fatalf("fresh reading: ranOutAt = %v; want %v", got, at)
	}
	for _, name := range []string{"stale", "errored", "expired", "cached"} {
		if got := tr.ranOutAt(name, windowAt(100, reset)); !got.IsZero() {
			t.Fatalf("%s reading counted as an observation: ranOutAt = %v", name, got)
		}
	}
}

func TestExhaustionPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)

	var first exhaustionTracker
	first.load(path, testMaxGap)
	first.observeFetched(map[string]*provider.UsageData{"claude": {FetchedAt: base, Windows: []provider.UsageWindow{windowAt(80, reset)}}}, nil, base, testMaxGap)
	first.observeFetched(map[string]*provider.UsageData{"claude": {FetchedAt: base.Add(5 * time.Minute), Windows: []provider.UsageWindow{windowAt(100, reset)}}}, nil, base.Add(5*time.Minute), testMaxGap)

	var second exhaustionTracker
	second.load(path, testMaxGap)
	if got := second.ranOutAt("claude", windowAt(100, reset)); !got.Equal(base.Add(5 * time.Minute)) {
		t.Fatalf("after restart ranOutAt = %v; want %v", got, base.Add(5*time.Minute))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("log mode = %v; want 0600", mode)
	}
}

func TestExhaustionCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	reset := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)

	var tr exhaustionTracker
	tr.load(path, testMaxGap)
	if got := tr.ranOutAt("claude", windowAt(100, reset)); !got.IsZero() {
		t.Fatalf("corrupt file produced ranOutAt = %v", got)
	}
	if loaded := loadExhaustionLog(filepath.Join(t.TempDir(), "missing.json")); len(loaded.Windows) != 0 {
		t.Fatalf("missing file loaded %d windows", len(loaded.Windows))
	}
}

func TestExhaustionPrunesPeriodsEndedMoreThanADayAgo(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var l exhaustionLog
	l.observe("claude", windowAt(100, now.Add(-25*time.Hour)), now.Add(-26*time.Hour))
	l.observe("claude:dondo", windowAt(100, now.Add(-23*time.Hour)), now.Add(-24*time.Hour))
	l.observe("codex", windowAt(100, now.Add(time.Hour)), now)

	path := filepath.Join(t.TempDir(), "exhaustion.json")
	if err := l.save(path, now); err != nil {
		t.Fatal(err)
	}
	loaded := loadExhaustionLog(path)
	if _, ok := loaded.Windows[exhaustionKey("claude", "5h")]; ok {
		t.Fatal("period that ended 25h ago was kept")
	}
	for _, source := range []string{"claude:dondo", "codex"} {
		if _, ok := loaded.Windows[exhaustionKey(source, "5h")]; !ok {
			t.Fatalf("%s period was pruned; it ended less than a day ago or is current", source)
		}
	}
}

func TestRunOutLabelsNameTheTimeTheTrayRecorded(t *testing.T) {
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 9, 40, 0, 0, time.Local)
	if got := ranOutLabel(at, today); got != "out since 09:40" {
		t.Fatalf("ranOutLabel today = %q", got)
	}
	if got := ranOutLabel(time.Time{}, today); got != "out now" {
		t.Fatalf("ranOutLabel without a time = %q", got)
	}
	yesterday := time.Date(2026, 10, 21, 9, 40, 0, 0, time.Local)
	if got := runOutSince(yesterday, time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)); got != "Oct 21 09:40" {
		t.Fatalf("runOutSince on an earlier day = %q", got)
	}
	if got := ranOutNote("out now (2h00m until reset)", at, today); got != "out since 09:40 (2h00m until reset)" {
		t.Fatalf("ranOutNote = %q", got)
	}
	if got := ranOutNote("runs out in 1h", at, today); got != "runs out in 1h" {
		t.Fatalf("ranOutNote changed a future run-out note: %q", got)
	}
}

func TestTrayRowShowsWhenAWindowRanOut(t *testing.T) {
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 9, 40, 0, 0, time.Local)
	row := meter.Row{Code: "5h", Used: "100%", RunsOut: "now", RanOutAt: at}
	widths := trayWidths{used: len("100%"), forecast: len("100%")}
	if got := trayRowText(row, widths); !strings.Contains(got, "out since 09:40") || strings.Contains(got, "out now") {
		t.Fatalf("row with a known time = %q", got)
	}
	row.RanOutAt = time.Time{}
	if got := trayRowText(row, widths); !strings.Contains(got, "out now") {
		t.Fatalf("row without a known time = %q", got)
	}
}

func TestTooltipShowsWhenAWindowRanOut(t *testing.T) {
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 9, 40, 0, 0, time.Local)
	window := provider.UsageWindow{Name: "7d", ResetsAt: time.Now().Add(2 * time.Hour)}
	proj := forecast.Projection{ProjectedPct: 180, WillLastToReset: false, RunsOutEarlyBy: 2 * time.Hour}

	if got := compactIconTooltip("Codex 7-Day", window, proj, at); !strings.Contains(got, "Out since 09:40 (2h00m until reset)") {
		t.Fatalf("tooltip with a known time = %q", got)
	}
	if got := compactIconTooltip("Codex 7-Day", window, proj, time.Time{}); !strings.Contains(got, "Out now (2h00m until reset)") {
		t.Fatalf("tooltip without a known time = %q", got)
	}
}

func TestTrayMenuRowsUsesTheObservedTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	windowExhaustion.load(path, testMaxGap)
	t.Cleanup(func() { windowExhaustion.load("", 0) })

	now := time.Now()
	reset := now.Add(3 * time.Hour)
	before, after := now.Add(-10*time.Minute), now.Add(-5*time.Minute)
	windowExhaustion.observeFetched(map[string]*provider.UsageData{
		"a": {FetchedAt: before, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 80, ResetsAt: reset}}},
	}, nil, before, testMaxGap)
	windowExhaustion.observeFetched(map[string]*provider.UsageData{
		"a": {FetchedAt: after, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: reset}}},
	}, nil, after, testMaxGap)

	menus := map[string]*providerMenuItems{"a": {windowItems: make([]*systray.MenuItem, maxWindowItems)}}
	rows := trayMenuRows(map[string]*provider.UsageData{
		"a": {FetchedAt: after, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: reset}}},
	}, menus)
	want := "out since " + runOutSince(after, now)
	if len(rows["a"]) != 1 || !strings.Contains(rows["a"][0], want) {
		t.Fatalf("menu rows = %q; want %q", rows["a"], want)
	}
}
