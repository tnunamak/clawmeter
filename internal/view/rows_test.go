package view

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestRowStatesFollowTheSnapshot(t *testing.T) {
	// 72% used two days into a seven-day window projects 252%.
	seven := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 72, ResetsAt: at.Add(5 * 24 * time.Hour)}
	row := rowFor(t, seven)
	if row.Code != "7d" || row.Forecast != "252%" || !row.Over || row.Used != "72%" || row.Resets != "5d" || row.RunsOut == "" {
		t.Fatalf("over-limit row = %+v", row)
	}
	seven.Utilization = 10
	if row := rowFor(t, seven); row.Forecast != "35%" || row.Over || row.RunsOut != "" {
		t.Fatalf("under-limit row = %+v", row)
	}
	early := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 1, ResetsAt: at.Add(7*24*time.Hour - time.Minute)}
	if row := rowFor(t, early); row.Forecast != "" || row.ForecastState != "unknown" {
		t.Fatalf("no forecast yet = %+v", row)
	}
	// Used up minutes into a window: no forecast yet, but out now.
	spent := provider.UsageWindow{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: at.Add(299 * time.Minute)}
	if row := rowFor(t, spent); row.ForecastState != "unknown" || row.RunsOut != "now" {
		t.Fatalf("exhausted without a forecast = %+v", row)
	}
	policy := provider.UsageWindow{Name: "daily", Utilization: 40, ResetPolicy: "resets daily"}
	if row := rowFor(t, policy); !row.ResetPolicy || row.Resets != "resets daily" || row.Forecast != "" || row.ForecastState != "" || row.HasPace() {
		t.Fatalf("policy row = %+v", row)
	}
	money := provider.UsageWindow{Name: "extra", DisplayName: "Extra usage", Utilization: 100, Limit: 2000, Used: 2004, Currency: "USD"}
	if row := rowFor(t, money); row.Line == "" {
		t.Fatalf("money-only row = %+v", row)
	}
}

// A stale reading keeps its windows but makes no forecast and no run-out,
// even when the last reading was at 100%.
func TestStaleReadingHasNoForecastOrRunOut(t *testing.T) {
	data := &provider.UsageData{
		Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: at.Add(-time.Hour),
		Windows: []provider.UsageWindow{{Name: "5h", Length: forecast.FiveHourWindow, Utilization: 100, ResetsAt: at.Add(time.Hour)}},
	}
	snap := Build(inputs(map[string]*provider.UsageData{"a": data}), at)
	acct := account(t, snap, "a")
	row := acct.Rows()[0]
	if row.ForecastState != "stale" || row.Forecast != "" || row.RunsOut != "" {
		t.Fatalf("stale row = %+v; a stale reading must not forecast or run out", row)
	}
	if row.Used != "100%" || row.Resets != "1h" {
		t.Fatalf("stale row = %+v; last values should still show", row)
	}
}

func TestRowsNameTheModelsOnlyWhenTwoWindowsShareALength(t *testing.T) {
	reset := at.Add(48 * time.Hour)
	all := provider.UsageWindow{Name: "7d All", DisplayName: "7 days (all models)", Length: 7 * 24 * time.Hour, Utilization: 30, ResetsAt: reset}
	fable := provider.UsageWindow{Name: "7d Fable", DisplayName: "7 days (Fable)", Length: 7 * 24 * time.Hour, Utilization: 20, ResetsAt: reset}
	session := provider.UsageWindow{Name: "5h", DisplayName: "5 hours", Length: 5 * time.Hour, Utilization: 10, ResetsAt: at.Add(time.Hour)}
	bonus := provider.UsageWindow{Name: "bonus", DisplayName: "Bonus", Utilization: 5, ResetsAt: reset}
	rows := func(windows ...provider.UsageWindow) []rowsLine {
		snap := Build(inputs(map[string]*provider.UsageData{"a": fresh("a", windows...)}), at)
		var out []rowsLine
		for _, r := range account(t, snap, "a").Rows() {
			out = append(out, rowsLine{code: r.Code, qualifier: r.Qualifier})
		}
		return out
	}
	if got := rows(session, all); got[0].code != "5h" || got[1].code != "7d" || got[1].qualifier != "" {
		t.Fatalf("one seven-day window: %+v", got)
	}
	if got := rows(all, fable, bonus); got[0].qualifier != "all models" || got[1].qualifier != "Fable" || got[2].code != "Bonus" || got[2].qualifier != "" {
		t.Fatalf("two seven-day windows: %+v", got)
	}
	if got := rows(fable); got[0].qualifier != "Fable" {
		t.Fatalf("a model-scoped window keeps its model: %+v", got)
	}
}

type rowsLine struct{ code, qualifier string }

// rowFor is the list row of the only window in a fresh reading.
func rowFor(t *testing.T, w provider.UsageWindow) meter.Row {
	t.Helper()
	snap := Build(inputs(map[string]*provider.UsageData{"a": fresh("a", w)}), at)
	return account(t, snap, "a").Rows()[0]
}

// BlockedFor is the time before the reset that the window is, or will be,
// used up: the early-by time for runs_out, the time left for out.
func TestBlockedForNamesHowLongBeforeTheResetTheWindowIsBlocked(t *testing.T) {
	runsOut := rowWindow(t, fiveHour(80, midWindow))
	if runsOut.Exhaustion.State != ExhaustionRunsOut || runsOut.Exhaustion.BlockedFor != 2*time.Hour+30*time.Minute {
		t.Fatalf("runs_out = %+v; want blocked for 2h30m", runsOut.Exhaustion)
	}
	out := rowWindow(t, fiveHour(100, midWindow))
	if out.Exhaustion.State != ExhaustionOut || out.Exhaustion.BlockedFor != midWindow {
		t.Fatalf("out = %+v; want blocked for the 3h left", out.Exhaustion)
	}
	warm := rowWindow(t, fiveHour(10, midWindow))
	if warm.Exhaustion.BlockedFor != 0 {
		t.Fatalf("lasting window blocked for %v; want none", warm.Exhaustion.BlockedFor)
	}
}

// rowWindow is the one window of a fresh reading.
func rowWindow(t *testing.T, w provider.UsageWindow) Window {
	t.Helper()
	snap := Build(inputs(map[string]*provider.UsageData{"a": fresh("a", w)}), at)
	return account(t, snap, "a").Windows[0]
}

// An expired credential is expired even when its error text is also set, as
// the tray and the CLI always classify it.
func TestExpiredTakesPrecedenceOverErrorText(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", IsExpired: true, Error: "token expired", FetchedAt: at.Add(-time.Minute)}
	snap := Build(inputs(map[string]*provider.UsageData{"a": data}), at)
	if got := account(t, snap, "a").Reading.State; got != ReadingExpired {
		t.Fatalf("reading = %v; want expired", got)
	}
}
