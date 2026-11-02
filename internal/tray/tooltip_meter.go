package tray

import (
	"math"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/view"
)

// tooltipCells shares the selected window's menu/CLI bar, including pace
// and the forecast state already decided by the snapshot.
func tooltipCells(acct *view.Account, window view.Window, at time.Time) []meter.Cell {
	if acct.Reading.State != view.ReadingFresh && acct.Reading.State != view.ReadingStale {
		return nil
	}
	if acct.Reading.ObservedAt.IsZero() || acct.Reading.ObservedAt.After(at) ||
		(window.Reset.Known && window.Reset.In <= 0) || math.IsNaN(window.Used) || math.IsInf(window.Used, 0) {
		return nil
	}
	if window.HasPace && (math.IsNaN(window.Pace) || math.IsInf(window.Pace, 0)) {
		return nil
	}
	if window.Reset.Known && window.Forecast.State == view.ForecastKnown &&
		(math.IsNaN(window.Forecast.Pct) || math.IsInf(window.Forecast.Pct, 0)) {
		return nil
	}
	return window.Row(false).Bar
}
