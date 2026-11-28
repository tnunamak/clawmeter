package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// A window that has run for 6 of its 168 hours still gets a forecast in `clawmeter --plain`: an early number beats no number.
// 14% used after 6 hours projects 392% at reset, and the line must say so, with the basis, instead of "forecast soon".
func TestPlainYoungWindowStillShowsAForecast(t *testing.T) {
	now := time.Now()
	window := provider.UsageWindow{Name: "7d", Utilization: 14, ResetsAt: now.Add(162 * time.Hour)}
	pf := ProviderFormatter{Display: "Test", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{window}}}
	got := pf.FormatPlain()
	for _, want := range []string{"est.", "average since reset"} {
		if !strings.Contains(got, want) {
			t.Errorf("plain output for a 7d window 6h in = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "forecast soon") {
		t.Errorf("plain output for a 7d window 6h in = %q, must not say forecast soon", got)
	}
}

// Only the first hour of a window has no forecast, because one sample of usage says nothing yet.
func TestPlainFirstHourOfAWindowSaysForecastSoon(t *testing.T) {
	now := time.Now()
	window := provider.UsageWindow{Name: "7d", Utilization: 1, ResetsAt: now.Add(168*time.Hour - 20*time.Minute)}
	pf := ProviderFormatter{Display: "Test", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{window}}}
	if got := pf.FormatPlain(); !strings.Contains(got, "forecast soon") {
		t.Errorf("plain output for a 7d window 20 min in = %q, want it to say forecast soon", got)
	}
}
