package meter

import (
	"math"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestBarFillsAFixedScale(t *testing.T) {
	for _, tc := range []struct {
		used, projected float64
		bar             string
	}{
		{42, 80, "████▒▒▒▒░░"},
		{61, 104, "██████▒▒▒▒"},
		{68, 213, "███████▒▒▒"},
		{0, 0, "░░░░░░░░░░"},
		{120, 300, "██████████"},
		{50, 40, "█████░░░░░"},
		{math.NaN(), math.NaN(), "░░░░░░░░░░"},
		{40, math.NaN(), "████░░░░░░"},
	} {
		if got := BarText(Bar(tc.used, tc.projected, 0, false)); got != tc.bar {
			t.Errorf("Bar(%v, %v) = %q, want %q", tc.used, tc.projected, got, tc.bar)
		}
	}
}

func TestBarHandlesInvalidUtilization(t *testing.T) {
	for _, pct := range []float64{-100, -5, math.NaN(), math.Inf(1), math.Inf(-1), 0, 100, 150} {
		got := BarText(Bar(pct, pct, pct, false))
		if n := len([]rune(got)); n != BarCells {
			t.Errorf("Bar(%v) width = %d, want %d", pct, n, BarCells)
		}
		if (math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 0) && got != "░░░░░░░░░░" {
			t.Errorf("Bar(%v) = %q, want an empty bar", pct, got)
		}
	}
}

func TestBarMarksPaceAndSplitsUsageAroundIt(t *testing.T) {
	if got := BarText(Bar(0, 0, 0, true)); got != "┃░░░░░░░░░░" {
		t.Fatalf("window just started = %q", got)
	}
	if got := BarText(Bar(100, 100, 100, true)); got != "██████████┃" {
		t.Fatalf("window about to reset = %q", got)
	}
	// 50% used where an even pace is at 30%: two cells ahead of pace.
	bar := Bar(50, 80, 30, true)
	if got := BarText(bar); got != "███┃██▒▒▒░░" {
		t.Fatalf("ahead of pace = %q", got)
	}
	kinds := []CellKind{Used, Used, Used, Pace, Ahead, Ahead, Projected, Projected, Projected, Free, Free}
	for i, cell := range bar {
		if cell.Kind != kinds[i] || cell.Behind {
			t.Fatalf("cell %d = %+v, want kind %v", i, cell, kinds[i])
		}
	}
	// 20% used where an even pace is at 60%: the gap up to the mark is behind.
	bar = Bar(20, 40, 60, true)
	for i, cell := range bar {
		if want := i >= 2 && i < 6; cell.Behind != want {
			t.Fatalf("cell %d behind = %v, want %v (%q)", i, cell.Behind, want, BarText(bar))
		}
	}
}

func TestShownDropsIdleWindowsButNeverAll(t *testing.T) {
	soon := time.Now().Add(24 * time.Hour)
	used := provider.UsageWindow{Name: "seven_day", Length: 7 * 24 * time.Hour, Utilization: 40, ResetsAt: soon}
	idle := provider.UsageWindow{Name: "seven_day_fable", Length: 7 * 24 * time.Hour, Utilization: 0, ResetsAt: soon}
	if got := Shown([]provider.UsageWindow{used, idle}); len(got) != 1 || got[0].Name != used.Name {
		t.Fatalf("windows = %+v", got)
	}
	if got := Shown([]provider.UsageWindow{idle}); len(got) != 1 {
		t.Fatalf("an all-idle provider lost its rows: %+v", got)
	}
}

func TestDurationUsesAtMostTwoUnits(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{2*time.Hour + 23*time.Minute, "2h 23m"},
		{time.Hour + time.Minute, "1h 1m"},
		{2 * time.Hour, "2h"},
		{59 * time.Minute, "59m"},
		{6*24*time.Hour + 16*time.Hour + 20*time.Minute, "6d 16h"},
		{7 * 24 * time.Hour, "7d"},
		{20 * time.Second, "<1m"},
		{30 * time.Second, "<1m"},
		{59 * time.Second, "<1m"},
		{time.Minute, "1m"},
		{-time.Minute, "<1m"},
	} {
		if got := Duration(tc.d); got != tc.want {
			t.Errorf("Duration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestNewRowStatesForecastUseAtReset(t *testing.T) {
	// 72% used two days into a seven-day window projects 252%.
	window := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 72, ResetsAt: time.Now().Add(5 * 24 * time.Hour)}
	row := NewRow(window, project(window), false)
	if row.Code != "7d" || row.Forecast != "252%" || !row.Over || row.Used != "72%" || row.Resets != "5d" || row.RunsOut == "" {
		t.Fatalf("over-limit row = %+v", row)
	}
	window.Utilization = 10
	if row := NewRow(window, project(window), false); row.Forecast != "35%" || row.Over || row.RunsOut != "" {
		t.Fatalf("under-limit row = %+v", row)
	}
	early := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 1, ResetsAt: time.Now().Add(7*24*time.Hour - time.Minute)}
	if row := NewRow(early, project(early), false); row.Forecast != "" || row.ForecastState != "unknown" {
		t.Fatalf("no forecast yet = %+v", row)
	}
	// Used up minutes into a window: no forecast yet, but out now.
	spent := provider.UsageWindow{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: time.Now().Add(299 * time.Minute)}
	if row := NewRow(spent, project(spent), false); row.ForecastState != "unknown" || row.RunsOut != "now" {
		t.Fatalf("exhausted without a forecast = %+v", row)
	}
	policy := provider.UsageWindow{Name: "daily", Utilization: 40, ResetPolicy: "resets daily"}
	if row := NewRow(policy, forecast.Projection{}, false); !row.ResetPolicy || row.Resets != "resets daily" || row.Forecast != "" || row.ForecastState != "" || row.HasPace() {
		t.Fatalf("policy row = %+v", row)
	}
	money := provider.UsageWindow{Name: "extra", DisplayName: "Extra usage", Utilization: 100, Limit: 2000, Used: 2004, Currency: "USD"}
	if row := NewRow(money, forecast.Projection{}, false); row.Line == "" {
		t.Fatalf("money-only row = %+v", row)
	}
}

func TestRowsNameTheModelsOnlyWhenTwoWindowsShareALength(t *testing.T) {
	reset := time.Now().Add(48 * time.Hour)
	all := provider.UsageWindow{Name: "7d All", DisplayName: "7 days (all models)", Length: 7 * 24 * time.Hour, Utilization: 30, ResetsAt: reset}
	fable := provider.UsageWindow{Name: "7d Fable", DisplayName: "7 days (Fable)", Length: 7 * 24 * time.Hour, Utilization: 20, ResetsAt: reset}
	session := provider.UsageWindow{Name: "5h", DisplayName: "5 hours", Length: 5 * time.Hour, Utilization: 10, ResetsAt: time.Now().Add(time.Hour)}
	bonus := provider.UsageWindow{Name: "bonus", DisplayName: "Bonus", Utilization: 5, ResetsAt: reset}
	rows := func(windows ...provider.UsageWindow) []Row {
		projections := make([]forecast.Projection, len(windows))
		for i, window := range windows {
			projections[i] = project(window)
		}
		return Rows(windows, projections)
	}
	if got := rows(session, all); got[0].Code != "5h" || got[1].Code != "7d" || got[1].Qualifier != "" {
		t.Fatalf("one seven-day window: %+v", got)
	}
	if got := rows(all, fable, bonus); got[0].Qualifier != "all models" || got[1].Qualifier != "Fable" || got[2].Code != "Bonus" || got[2].Qualifier != "" {
		t.Fatalf("two seven-day windows: %+v", got)
	}
	if got := rows(fable); got[0].Qualifier != "Fable" {
		t.Fatalf("a model-scoped window keeps its model: %+v", got)
	}
}

func project(window provider.UsageWindow) forecast.Projection {
	return forecast.Project(window.Utilization, window.ResetsAt, forecast.WindowLength(window))
}
