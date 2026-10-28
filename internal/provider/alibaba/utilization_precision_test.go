package alibaba

import (
	"math"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestUtilizationPrecisionPreservesObservedExhaustion(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	p := New(config.ProviderConfig{})
	p.now = func() time.Time { return now }
	for _, tc := range []struct {
		name    string
		used    float64
		pct     float64
		state   view.ExhaustionState
		display string
	}{
		{"one unit remains", 999999, 99.9999, view.ExhaustionRunsOut, "<100%"},
		{"exactly exhausted", 1000000, 100, view.ExhaustionOut, "100%"},
		{"room remains", 250000, 25, view.ExhaustionLasts, "25%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := p.transformQuota(map[string]any{
				"per5HourUsedQuota":            tc.used,
				"per5HourTotalQuota":           1000000.0,
				"per5HourQuotaNextRefreshTime": float64(now.Add(3 * time.Hour).UnixMilli()),
			})
			if len(data.Windows) != 1 {
				t.Fatalf("windows = %+v, want the five-hour quota", data.Windows)
			}
			w := data.Windows[0]
			if math.Abs(w.Utilization-tc.pct) > 1e-10 {
				t.Errorf("stored utilization = %.10f, want %.10f", w.Utilization, tc.pct)
			}
			proj := forecast.ProjectAt(w.Utilization, w.ResetsAt, forecast.WindowLength(w), now)
			snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"alibaba": data}, MaxReadingAge: time.Minute}, now)
			if got := snap.Accounts[0].Windows[0].Exhaustion.State; got != tc.state {
				t.Errorf("exhaustion = %s, want %s; projection = %+v", got, tc.state, proj)
			}
			if got := format.Percent(w.Utilization); got != tc.display {
				t.Errorf("display = %q, want %q", got, tc.display)
			}
			if tc.state == view.ExhaustionRunsOut && proj.RunsOutIn <= 0 {
				t.Errorf("quota with one unit left must have a future ETA: %+v", proj)
			}
			if tc.state == view.ExhaustionOut && (proj.WillLastToReset || proj.RunsOutIn != 0) {
				t.Errorf("observed empty quota must be out now: %+v", proj)
			}
			if tc.state == view.ExhaustionLasts && !proj.WillLastToReset {
				t.Errorf("below-limit control must last to reset: %+v", proj)
			}
			if tc.used < 1000000 {
				out := provider.UsageWindow{Name: "daily", Utilization: 100}
				if got := forecast.CompareWindowRisk(w, proj, out, forecast.Projection{Unknown: true}); got <= 0 {
					t.Errorf("quota with remaining units outranked observed exhaustion: comparison = %d", got)
				}
			}
		})
	}
}
