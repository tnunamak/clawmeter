package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// A 7-day window six hours in used 14%: the average since reset projected "est. 215% at reset" for Dondo's account.
// `clawmeter --plain` must say the forecast is not ready yet until a tenth of the window has elapsed.
func TestPlainEarlyWindowShowsNoEstimate(t *testing.T) {
	now := time.Now()
	window := provider.UsageWindow{Name: "7d", Utilization: 14, ResetsAt: now.Add(162 * time.Hour)}
	pf := ProviderFormatter{Display: "Test", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{window}}}
	got := pf.FormatPlain()
	if !strings.Contains(got, "forecast soon") {
		t.Errorf("plain output for a 7d window 6h in = %q, want it to say forecast soon", got)
	}
	for _, forbidden := range []string{"est.", "average since reset", "runs out"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("plain output for a 7d window 6h in = %q, must not contain %q", got, forbidden)
		}
	}
}

// Past the floor (a tenth of the window, at most a day) the same window gets its average-based estimate back.
func TestPlainWindowPastTheFloorShowsAnEstimate(t *testing.T) {
	now := time.Now()
	window := provider.UsageWindow{Name: "7d", Utilization: 14, ResetsAt: now.Add(168*time.Hour - 30*time.Hour)}
	pf := ProviderFormatter{Display: "Test", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{window}}}
	if got := pf.FormatPlain(); !strings.Contains(got, "est.") || strings.Contains(got, "forecast soon") {
		t.Errorf("plain output for a 7d window 30h in = %q, want an estimate and no forecast soon", got)
	}
}
