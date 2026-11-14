package cli

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/view"
)

// The published v1 forecast enum has no pending value, so machine output reports
// a window whose reset has passed as stale, as the legacy indicator always has.
func TestJSONForecastStateReportsPendingAsStale(t *testing.T) {
	want := map[view.ForecastState]string{
		view.ForecastKnown:   "known",
		view.ForecastWarming: "warming",
		view.ForecastStale:   "stale",
		view.ForecastPending: "stale",
		view.ForecastNone:    "none",
	}
	for state, expected := range want {
		if got := jsonForecastState(state); got != expected {
			t.Errorf("jsonForecastState(%q) = %q, want %q", state, got, expected)
		}
	}
}
