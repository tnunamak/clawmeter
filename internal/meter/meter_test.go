package meter

import (
	"math"
	"testing"
	"time"

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

func TestDurationPadsTwoUnitsToAFixedWidth(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{2*time.Hour + 4*time.Minute, "\u20072h04m"},
		{12*time.Hour + 34*time.Minute, "12h34m"},
		{9 * time.Minute, "\u20070h09m"},
		{3*24*time.Hour + 18*time.Hour, "\u20073d18h"},
		{7 * 24 * time.Hour, "\u20077d\u20070h"},
		{-time.Minute, "\u20070h00m"},
	} {
		if got := Duration(tc.d, "\u2007"); got != tc.want {
			t.Errorf("Duration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
