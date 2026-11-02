package tray

import (
	"math"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/view"
)

// tooltipBar shares the selected window's menu/CLI bar, including pace
// and the forecast state already decided by the snapshot.
func tooltipBar(acct *view.Account, window view.Window, at time.Time) string {
	if acct.Reading.State != view.ReadingFresh && acct.Reading.State != view.ReadingStale {
		return ""
	}
	if acct.Reading.ObservedAt.IsZero() || acct.Reading.ObservedAt.After(at) ||
		(window.Reset.Known && window.Reset.In <= 0) || math.IsNaN(window.Used) || math.IsInf(window.Used, 0) {
		return ""
	}
	if window.HasPace && (math.IsNaN(window.Pace) || math.IsInf(window.Pace, 0)) {
		return ""
	}
	if window.Reset.Known && window.Forecast.State == view.ForecastKnown &&
		(math.IsNaN(window.Forecast.Pct) || math.IsInf(window.Forecast.Pct, 0)) {
		return ""
	}
	return meter.BarText(window.Row(false).Bar)
}
