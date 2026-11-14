package view

import (
	"testing"
	"time"
)

// With a one-hour poll the reading bound is two hours, so only the one-hour
// retained horizon ends a retained forecast.
func TestRetainedHorizonBoundsAnHourlyPoll(t *testing.T) {
	in := Inputs{MaxReadingAge: 2*time.Hour + time.Minute, RetainedForecast: RetainedForecastAge}
	if !forecastable(in, ReadingStale, at, 59*time.Minute) {
		t.Fatal("a retained reading within the horizon does not forecast")
	}
	if forecastable(in, ReadingStale, at, 61*time.Minute) {
		t.Fatal("a retained reading past the one-hour horizon forecasts")
	}
	if !forecastable(in, ReadingFresh, at, 90*time.Minute) {
		t.Fatal("a fresh reading inside the two-hour bound does not forecast")
	}
}
