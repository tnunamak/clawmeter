package view

import (
	"math"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestAuditM3SuccessfulSampleCountsDown(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", FetchedAt: at, Windows: []provider.UsageWindow{fiveHour(80, midWindow)}}
	in := Inputs{Results: map[string]*provider.UsageData{"a": data}, MaxReadingAge: 10 * time.Minute}
	first := Build(in, at).Accounts[0].Windows[0]
	later := Build(in, at.Add(5*time.Minute)).Accounts[0].Windows[0]
	if first.Forecast.Pct != 200 || later.Forecast.Pct != 200 || first.Exhaustion.RunsOutIn != 30*time.Minute || later.Exhaustion.RunsOutIn != 25*time.Minute || first.Exhaustion.BlockedFor != later.Exhaustion.BlockedFor {
		t.Fatalf("same observation drifted: first=%+v later=%+v", first, later)
	}
}

func TestAuditM3EstimatePassed(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", FetchedAt: at, Windows: []provider.UsageWindow{fiveHour(99.6, midWindow)}}
	in := Inputs{Results: map[string]*provider.UsageData{"a": data}, MaxReadingAge: 10 * time.Minute}
	passed := Build(in, at.Add(45*time.Second)).Accounts[0].Windows[0]
	if passed.Exhaustion.State != ExhaustionUnknown || math.Abs(passed.Forecast.Pct-249) > 1e-9 {
		t.Fatalf("passed estimate must not reschedule or assert observed exhaustion: %+v", passed)
	}
}

func TestAuditM3SuccessfulSampleRequiresValidAge(t *testing.T) {
	for _, fetched := range []time.Time{{}, at.Add(time.Second), at.Add(-10*time.Minute - time.Nanosecond)} {
		in := Inputs{Results: map[string]*provider.UsageData{"a": {Provider: "claude", FetchedAt: fetched, Windows: []provider.UsageWindow{fiveHour(100, midWindow)}}}, MaxReadingAge: 10 * time.Minute}
		a := Build(in, at).Accounts[0]
		w := a.Windows[0]
		if a.Reading.State != ReadingStale || w.Forecast.State != ForecastStale || w.Exhaustion.State != ExhaustionUnknown {
			t.Errorf("invalid observation %v: reading=%+v window=%+v", fetched, a.Reading, w)
		}
	}
}

func TestAuditM5ObservedOutBeatsHealthyReset(t *testing.T) {
	for _, keys := range [][2]string{{"claude", "openrouter"}, {"claude:one", "claude:two"}} {
		in := Inputs{Results: map[string]*provider.UsageData{
			keys[0]: {Provider: "claude", FetchedAt: at, Windows: []provider.UsageWindow{fiveHour(20, midWindow)}},
			keys[1]: {Provider: "openrouter", FetchedAt: at, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetPolicy: "daily"}}},
		}, MaxReadingAge: 10 * time.Minute}
		if keys[0] == "claude:one" {
			in.Results[keys[1]].Provider = "claude"
			in.FamilySourceCounts = map[string]int{"claude": 2}
			in.SourceLabels = map[string]string{keys[0]: "One", keys[1]: "Two"}
		}
		snap := Build(in, at)
		if snap.Worst == nil || snap.Worst.AccountKey != keys[1] {
			t.Errorf("worst=%+v, want exhausted account %s", snap.Worst, keys[1])
		}
		w := account(t, snap, keys[1]).Windows[0]
		if w.Exhaustion.State != ExhaustionOut || w.Reset.Known || w.Exhaustion.RunsOutIn != 0 || w.Forecast.State != ForecastNone {
			t.Fatalf("observed out must not invent reset/forecast: %+v", w)
		}
	}
}

func TestAuditM3SuccessfulAgeBoundaries(t *testing.T) {
	data := &provider.UsageData{FetchedAt: at, Windows: []provider.UsageWindow{fiveHour(80, midWindow)}}
	in := Inputs{Results: map[string]*provider.UsageData{"a": data}, MaxReadingAge: 10 * time.Minute}
	if w := Build(in, at.Add(10*time.Minute)).Accounts[0].Windows[0]; w.Forecast.Pct != 200 || w.Exhaustion.RunsOutIn != 20*time.Minute {
		t.Fatalf("age boundary: %+v", w)
	}
	in.MaxReadingAge = 0
	if w := Build(in, at).Accounts[0].Windows[0]; w.Forecast.State != ForecastKnown {
		t.Fatalf("observation instant: %+v", w)
	}
	if w := Build(in, at.Add(time.Nanosecond)).Accounts[0].Windows[0]; w.Forecast.State != ForecastStale {
		t.Fatalf("zero allowance: %+v", w)
	}
}

func TestAuditN2SubminuteExhaustionRemainsFuture(t *testing.T) {
	in := Inputs{Results: map[string]*provider.UsageData{"a": {FetchedAt: at, Windows: []provider.UsageWindow{fiveHour(99.8, midWindow)}}}, MaxReadingAge: time.Minute}
	w := Build(in, at).Accounts[0].Windows[0]
	if w.Exhaustion.State != ExhaustionRunsOut || w.ExhaustionWords() != "runs out in <1m (3h00m before reset)" {
		t.Fatalf("future exhaustion=%+v words=%q", w.Exhaustion, w.ExhaustionWords())
	}
}
