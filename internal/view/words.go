package view

import (
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
)

// ForecastWords states a window's forecast in the words every surface uses:
// "est. 50% at reset" when known, "projection unknown" while the forecast
// cannot yet say, and "stale" for a window whose reading or reset is past.
// It is empty when the window has no reset.
func (w Window) ForecastWords() string {
	switch w.Forecast.State {
	case ForecastKnown:
		return forecast.PaceLabel(w.Forecast.Pct)
	case ForecastStale:
		return "stale"
	case ForecastWarming, ForecastNone:
		return "projection unknown"
	}
	return ""
}

// ExhaustionWords states how the window runs out, in lower case, or "" when
// there is nothing to say. "out now" comes with the time left until the reset
// when known; "runs out in 2h (1h before reset)" names the run-out and the
// time it leaves before the reset. Unknown and lasting windows say nothing
// here; surfaces word those themselves.
func (w Window) ExhaustionWords() string {
	switch w.Exhaustion.State {
	case ExhaustionOut:
		return outNowWords(w.Exhaustion.BlockedFor)
	case ExhaustionRunsOut:
		if w.Exhaustion.RunsOutIn <= 0 {
			return outNowWords(w.Exhaustion.BlockedFor)
		}
		note := "runs out in " + format.FormatDuration(w.Exhaustion.RunsOutIn)
		if w.Exhaustion.BlockedFor > 0 {
			note += " (" + format.FormatDuration(w.Exhaustion.BlockedFor) + " before reset)"
		}
		return note
	}
	return ""
}

// outNowWords is "out now", with the time left until the reset when it is known.
func outNowWords(blockedFor time.Duration) string {
	if blockedFor > 0 {
		return "out now (" + format.FormatDuration(blockedFor) + " until reset)"
	}
	return "out now"
}
