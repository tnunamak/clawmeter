package view

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestRetainedReadingForecast(t *testing.T) {
	for _, tc := range []struct {
		name       string
		age        time.Duration
		used       float64
		resetIn    time.Duration
		state      ForecastState
		exhaustion ExhaustionState
	}{
		{"just fetched", 0, 80, 3 * time.Hour, ForecastKnown, ExhaustionRunsOut},
		{"observed out", 2 * time.Minute, 100, 3 * time.Hour, ForecastKnown, ExhaustionOut},
		{"young", 2 * time.Minute, 80, 3 * time.Hour, ForecastKnown, ExhaustionRunsOut},
		{"at limit", 10 * time.Minute, 80, 3 * time.Hour, ForecastKnown, ExhaustionRunsOut},
		{"older", 10*time.Minute + time.Nanosecond, 80, 3 * time.Hour, ForecastStale, ExhaustionUnknown},
		{"runout exactly now", 10 * time.Minute, 75, 260 * time.Minute, ForecastKnown, ExhaustionUnknown},
		{"reset exactly now", 2 * time.Minute, 80, 0, ForecastStale, ExhaustionUnknown},
		{"runout passed", 2 * time.Minute, 99.9, 3 * time.Hour, ForecastKnown, ExhaustionUnknown},
		{"reset passed", 2 * time.Minute, 80, -time.Minute, ForecastStale, ExhaustionUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetched := at.Add(-tc.age)
			w := fiveHour(tc.used, tc.resetIn)
			data := &provider.UsageData{Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: fetched, Windows: []provider.UsageWindow{w}}
			in := inputs(map[string]*provider.UsageData{"a": data})
			in.MaxReadingAge = 10 * time.Minute
			acct := account(t, Build(in, at), "a")
			got := acct.Windows[0]
			if acct.Reading.State != ReadingStale || got.Forecast.State != tc.state || got.Exhaustion.State != tc.exhaustion {
				t.Fatalf("reading=%+v window=%+v", acct.Reading, got)
			}
			if tc.exhaustion == ExhaustionRunsOut {
				baseline := Build(in, fetched).Accounts[0].Windows[0]
				if got.Forecast.Pct != baseline.Forecast.Pct || got.Exhaustion.RunsOutIn != baseline.Exhaustion.RunsOutIn-tc.age || got.Exhaustion.BlockedFor != baseline.Exhaustion.BlockedFor {
					t.Fatalf("forecast/runout shifted incorrectly: got %+v, at fetch %+v", got, baseline)
				}
			}
		})
	}
}

func TestUnusableRetainedReadingsStayStale(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fetched time.Time
		err     string
		expired bool
	}{
		{"error", at.Add(-time.Minute), "poll failed", false},
		{"expired", at.Add(-time.Minute), "", true},
		{"missing fetch time", time.Time{}, "", false},
		{"future fetch time", at.Add(time.Minute), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := &provider.UsageData{Stale: true, Error: tc.err, IsExpired: tc.expired, FetchedAt: tc.fetched, Windows: []provider.UsageWindow{fiveHour(80, midWindow)}}
			in := Inputs{Results: map[string]*provider.UsageData{"a": data}, MaxReadingAge: 10 * time.Minute}
			w := Build(in, at).Accounts[0].Windows[0]
			if w.Forecast.State != ForecastStale || w.Exhaustion.State != ExhaustionUnknown {
				t.Fatalf("window=%+v", w)
			}
		})
	}
}

func TestRetainedReadingDoesNotWarmUpDuringGap(t *testing.T) {
	data := &provider.UsageData{Stale: true, FetchedAt: at.Add(-2 * time.Minute), Windows: []provider.UsageWindow{fiveHour(10, 269*time.Minute)}}
	w := Build(Inputs{Results: map[string]*provider.UsageData{"a": data}, MaxReadingAge: 10 * time.Minute}, at).Accounts[0].Windows[0]
	if w.Forecast.State != ForecastWarming || w.Exhaustion.State != ExhaustionUnknown {
		t.Fatalf("window=%+v", w)
	}
}
