package meter

import (
	"math"
	"testing"
	"time"
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
