package copilot

import (
	"math"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestAuditM2CalendarMonthForecast(t *testing.T) {
	for _, tc := range []struct {
		name, observed, reset string
		used, pct             float64
		days                  int
		state                 view.ForecastState
		exhaustion            view.ExhaustionState
	}{
		{"February", "2026-02-02T00:00:00Z", "2026-03-01T00:00:00Z", 9, 252, 28, view.ForecastKnown, view.ExhaustionRunsOut},
		{"October", "2026-10-03T00:00:00Z", "2026-11-01T00:00:00Z", 5, 77.5, 31, view.ForecastKnown, view.ExhaustionLasts},
		{"leap February", "2028-02-02T00:00:00Z", "2028-03-01T00:00:00Z", 9, 261, 29, view.ForecastKnown, view.ExhaustionRunsOut},
		{"April", "2026-04-02T00:00:00Z", "2026-05-01T00:00:00Z", 5, 150, 30, view.ForecastKnown, view.ExhaustionRunsOut},
		{"first hour", "2026-10-01T00:30:00Z", "2026-11-01T00:00:00Z", 1, 0, 31, view.ForecastWarming, view.ExhaustionUnknown},
		{"warm at one hour", "2026-10-01T01:00:00Z", "2026-11-01T00:00:00Z", 1, 744, 31, view.ForecastKnown, view.ExhaustionRunsOut},
		{"last hour", "2026-02-28T23:00:00Z", "2026-03-01T00:00:00Z", 50, 50 * 672.0 / 671, 28, view.ForecastKnown, view.ExhaustionLasts},
		{"at reset", "2026-03-01T00:00:00Z", "2026-03-01T00:00:00Z", 50, 0, 28, view.ForecastStale, view.ExhaustionUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.observed)
			remaining := 100 - tc.used
			data := (&Provider{}).transformUsage(&userResponse{QuotaResetDate: tc.reset, QuotaSnapshots: map[string]quotaSnapshot{"premium_interactions": {PercentRemaining: &remaining}}})
			data.FetchedAt = now
			if data.Windows[0].Length != time.Duration(tc.days)*24*time.Hour {
				t.Errorf("length=%v want %d days", data.Windows[0].Length, tc.days)
			}
			w := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"copilot": data}, MaxReadingAge: time.Minute}, now).Accounts[0].Windows[0]
			if w.Forecast.State != tc.state || math.Abs(w.Forecast.Pct-tc.pct) > 1e-8 || w.Exhaustion.State != tc.exhaustion {
				t.Fatalf("window=%+v want %s %.8f %s", w, tc.state, tc.pct, tc.exhaustion)
			}
			if tc.name == "February" && w.Exhaustion.RunsOutIn != 10*24*time.Hour+2*time.Hour+40*time.Minute {
				t.Fatalf("February exhaustion=%v", w.Exhaustion.RunsOutIn)
			}
		})
	}
}

func TestAuditM2UnexpectedCopilotBoundaryWithholdsForecast(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	remaining := 91.0
	data := (&Provider{}).transformUsage(&userResponse{QuotaResetDate: "2026-03-15", QuotaSnapshots: map[string]quotaSnapshot{"premium_interactions": {PercentRemaining: &remaining}}})
	data.FetchedAt = now
	w := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"copilot": data}, MaxReadingAge: time.Minute}, now).Accounts[0].Windows[0]
	if w.Forecast.State != view.ForecastNone || w.Exhaustion.State != view.ExhaustionUnknown {
		t.Fatalf("invented billing start: %+v", w)
	}
}
