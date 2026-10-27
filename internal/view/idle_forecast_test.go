package view

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestIdleMovingResetDoesNotPromiseForecastSoon(t *testing.T) {
	for _, at := range []time.Time{time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)} {
		w := provider.UsageWindow{Name: "pool", Length: 7 * 24 * time.Hour, ResetsAt: at.Add(7 * 24 * time.Hour)}
		got := buildWindow(Inputs{}, "antigravity", w, true, at, at)
		if got.ForecastWords() != "no usage yet" {
			t.Fatalf("words=%q forecast=%+v", got.ForecastWords(), got.Forecast)
		}
		if got.Reset.At != w.ResetsAt || got.Used != 0 || got.Exhaustion.State != ExhaustionUnknown {
			t.Fatalf("invented quota/reset/runout: %+v", got)
		}
	}
}

func TestZeroMatureAndPositiveYoungForecastsUnchanged(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		used  float64
		left  time.Duration
		words string
	}{
		{0, 6 * 24 * time.Hour, "est. 0% at reset"},
		{3, 7*24*time.Hour - 10*time.Minute, "forecast soon"},
	} {
		got := buildWindow(Inputs{}, "a", provider.UsageWindow{Name: "pool", Length: 7 * 24 * time.Hour, Utilization: tc.used, ResetsAt: at.Add(tc.left)}, true, at, at)
		if got.ForecastWords() != tc.words {
			t.Fatalf("used=%v words=%q", tc.used, got.ForecastWords())
		}
	}
}
