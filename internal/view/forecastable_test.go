package view

import (
	"testing"
	"time"
)

// A reading past the reading bound forecasts only when the caller allows
// retained forecasts, and then only up to RetainedForecastAge. A fresh reading
// never gets the retained allowance.
func TestForecastableBands(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    ReadingState
		age      time.Duration
		retained time.Duration
		want     bool
	}{
		{"fresh within the reading bound", ReadingFresh, 10 * time.Minute, 0, true},
		{"fresh past the reading bound", ReadingFresh, 10*time.Minute + time.Nanosecond, RetainedForecastAge, false},
		{"stale within the reading bound", ReadingStale, 2 * time.Minute, 0, true},
		{"stale past the reading bound, within horizon", ReadingStale, 11 * time.Minute, RetainedForecastAge, true},
		{"stale at the horizon", ReadingStale, RetainedForecastAge, RetainedForecastAge, true},
		{"stale past the horizon", ReadingStale, RetainedForecastAge + time.Nanosecond, RetainedForecastAge, false},
		{"stale past the reading bound, strict caller", ReadingStale, 11 * time.Minute, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Inputs{MaxReadingAge: 10 * time.Minute, RetainedForecast: tc.retained}
			if got := forecastable(in, tc.state, at, tc.age); got != tc.want {
				t.Fatalf("forecastable(age %v, retained %v) = %v, want %v", tc.age, tc.retained, got, tc.want)
			}
		})
	}
}

func TestForecastStateLapsed(t *testing.T) {
	for _, s := range []ForecastState{ForecastKnown, ForecastWarming, ForecastNone} {
		if s.Lapsed() {
			t.Errorf("%s reads as lapsed; only a stale or pending forecast is not current", s)
		}
	}
	for _, s := range []ForecastState{ForecastStale, ForecastPending} {
		if !s.Lapsed() {
			t.Errorf("%s does not read as lapsed; its usage is not current", s)
		}
	}
}
