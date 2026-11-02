package tray

import (
	"math"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/view"
)

// tooltipUsageBar shares the menu/CLI scale and glyphs. Only observed use is
// drawn; forecasts remain in words, and stale text qualifies last-known use.
func tooltipUsageBar(acct *view.Account, window view.Window, at time.Time) string {
	if acct.Reading.State != view.ReadingFresh && acct.Reading.State != view.ReadingStale {
		return ""
	}
	if acct.Reading.ObservedAt.IsZero() || acct.Reading.ObservedAt.After(at) ||
		(window.Reset.Known && window.Reset.In <= 0) || math.IsNaN(window.Used) || math.IsInf(window.Used, 0) {
		return ""
	}
	return meter.BarText(meter.Bar(window.Used, window.Used, 0, false))
}
