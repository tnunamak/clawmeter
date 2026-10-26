package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

// An expired credential with no quota windows is still the account the icon
// shows: the warning meter, not an empty one. It is what the tray drew before
// the snapshot, and an empty meter would hide the expiry.
func TestExpiredAccountWithNoWindowsDrawsTheWarningMeter(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{
		"claude": {Provider: "claude", IsExpired: true, FetchedAt: now.Add(-time.Hour)},
	}}, now)
	name, meter := IconState(snap)
	if name != "claude" || meter.UsagePct != 100 || meter.ExpectedPct != 100 || meter.RiskPct != 100 {
		t.Fatalf("IconState() = %s %+v; want claude with the warning meter", name, meter)
	}
}

// With an errored account and a healthy one, the icon names the healthy window,
// and an error with no windows draws no meter at all.
func TestErroredAccountWithNoWindowsDrawsNoMeter(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{
		"openai": {Provider: "openai", Error: "usage request failed (500)"},
	}}, now)
	name, meter := IconState(snap)
	if name != "openai" || meter.UsagePct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("IconState() = %s %+v; want openai with no meter", name, meter)
	}
}

func TestUpdateLineIsAddedOnlyForAVersion(t *testing.T) {
	if got := AppendUpdateLine("Claude: 5h", ""); got != "Claude: 5h" {
		t.Fatalf("AppendUpdateLine without a version = %q", got)
	}
	if got := AppendUpdateLine("Claude: 5h", "0.42.0"); got != "Claude: 5h\nUpdate available: 0.42.0" {
		t.Fatalf("AppendUpdateLine = %q", got)
	}
	if got := AppendUpdateLine("", "0.42.0"); !strings.HasPrefix(got, "Update available:") || strings.Contains(got, "\n") {
		t.Fatalf("AppendUpdateLine on an empty tooltip = %q", got)
	}
}

func TestMenuMoreCountsOnlyShownWindowsPastTheLimit(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var windows []provider.UsageWindow
	for i := 0; i < 10; i++ {
		windows = append(windows, provider.UsageWindow{Name: "w" + string(rune('a'+i)), Utilization: 50, ResetsAt: at.Add(time.Hour), Length: 5 * time.Hour})
	}
	windows = append(windows, provider.UsageWindow{Name: "idle", Utilization: 0, ResetsAt: at.Add(time.Hour), Length: 5 * time.Hour})
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"codex": {Provider: "openai", FetchedAt: at, Windows: windows}}}, at)
	if got := MenuMore(snap, 8)["codex"]; got != 2 {
		t.Fatalf("more = %d; want the two shown windows past eight rows, not the idle one", got)
	}
	if got := MenuMore(snap, 20)["codex"]; got != 0 {
		t.Fatalf("more = %d with room for every row", got)
	}
}

func TestForecastWordsKeepResetColumnsAligned(t *testing.T) {
	rows := []meter.Row{
		{Forecast: "99%", Resets: "1h"},
		{ForecastState: "forecast soon", Resets: "1h"},
		{ForecastState: "forecast stale", Resets: "1h"},
		{ForecastState: "no forecast", Resets: "1h"},
	}
	widths := trayColumnWidths(rows)
	want := -1
	for _, row := range rows {
		text := trayRowText(row, widths, time.Now())
		pos := len([]rune(strings.Split(text, "resets in")[0]))
		if want < 0 {
			want = pos
		}
		if pos != want {
			t.Fatalf("reset column at %d, want %d: %q", pos, want, text)
		}
	}
}

func TestPinnedRetainedIconAgreesWithTheSharedWindow(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 28, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		fetched   time.Time
		resetIn   time.Duration
		wantUsage bool
	}{
		{"young retained forecast", now.Add(-2 * time.Minute), 55 * time.Minute, true},
		{"old retained usage without forecast", now.Add(-11 * time.Minute), 55 * time.Minute, true},
		{"missing observation time", time.Time{}, 55 * time.Minute, false},
		{"reset passed", now.Add(-2 * time.Minute), -time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := map[string]*provider.UsageData{
				"claude:dondo": {Provider: "claude", SourceID: "dondo", Stale: true, Warning: "usage unavailable", FetchedAt: tc.fetched, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 10, ResetsAt: now.Add(tc.resetIn)}}},
				"openai":       {Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 30, ResetsAt: now.Add(time.Hour)}}},
			}
			snap := view.Build(view.Inputs{Results: results, MaxReadingAge: 10 * time.Minute, Pin: &view.Target{AccountKey: "claude:dondo", Window: "5h"}}, now)
			if snap.Selected == nil || snap.Selected.AccountKey != "claude:dondo" {
				t.Fatalf("lost pinned source: %+v", snap.Selected)
			}
			acct := accountNamed(snap, "claude:dondo")
			w := acct.Windows[0]
			name, got := IconState(snap)
			if name != "claude" || acct.Reading.State != view.ReadingStale || !strings.Contains(Tooltip(snap, results), "stale - showing last good data") {
				t.Fatal("retained meter lost its source or stale warning")
			}
			if !tc.wantUsage {
				if got.UsagePct != 0 || got.RiskPct != 0 || got.ShowExpected {
					t.Fatalf("unusable retained window = %+v", got)
				}
				return
			}
			wantRisk := 0.0
			if w.Forecast.State == view.ForecastKnown {
				wantRisk = w.Forecast.Pct
			}
			if got.UsagePct != w.Used || got.ExpectedPct != w.Pace || got.ShowExpected != w.HasPace || got.RiskPct != wantRisk || got.Label != "5H" {
				t.Fatalf("icon = %+v, shared window = %+v", got, w)
			}
			if !strings.Contains(strings.Join(MenuRows(snap, 8)["claude:dondo"], " "), "10% used") {
				t.Fatal("menu and icon disagree on retained usage")
			}
			if tc.name == "old retained usage without forecast" && w.Forecast.State != view.ForecastStale {
				t.Fatal("old retained forecast became current")
			}
		})
	}
}
