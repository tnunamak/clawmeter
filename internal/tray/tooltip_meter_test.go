package tray

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestTooltipMeterUsesTheSelectedRow(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	snapshot := func() view.Snapshot {
		return view.Snapshot{At: now, Selected: &view.Target{AccountKey: "work", Window: "5h", Reason: "pinned"}, Accounts: []view.Account{
			{Key: "other", Reading: view.Reading{State: view.ReadingFresh, ObservedAt: now}, Windows: []view.Window{{Name: "5h", Used: 100}}},
			{Key: "work", Label: "Work", Reading: view.Reading{State: view.ReadingFresh, ObservedAt: now}, Windows: []view.Window{{
				Name: "5h", Used: 30, Pace: 20, HasPace: true,
				Forecast: view.Forecast{State: view.ForecastKnown, Pct: 175}, Reset: view.Reset{Known: true, In: time.Hour},
			}}},
		}}
	}
	for _, tc := range []struct {
		name string
		tune func(*view.Snapshot)
		bar  string
	}{
		{"pinned forecast overflow, fixed scale", func(*view.Snapshot) {}, "██┃█▒▒▒▒▒▒▒"},
		{"bounded forecast", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Forecast.Pct = 60 }, "██┃█▒▒▒░░░░"},
		{"warming forecast", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Forecast.State = view.ForecastWarming }, "██┃█░░░░░░░"},
		{"retained known forecast", func(s *view.Snapshot) {
			s.Accounts[1].Reading.State = view.ReadingStale
			s.Accounts[1].Reading.ObservedAt = now.Add(-time.Minute)
		}, "██┃█▒▒▒▒▒▒▒"},
		{"stale withheld forecast", func(s *view.Snapshot) {
			s.Accounts[1].Reading.State = view.ReadingStale
			s.Accounts[1].Windows[0].Forecast.State = view.ForecastStale
		}, "██┃█░░░░░░░"},
		{"no forecast", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Forecast.State = view.ForecastNone }, "██┃█░░░░░░░"},
		{"no reset", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Reset.Known = false }, "██┃█░░░░░░░"},
		{"no pace", func(s *view.Snapshot) { s.Accounts[1].Windows[0].HasPace = false }, "███▒▒▒▒▒▒▒"},
		{"pace at end", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Pace = 100 }, "███▒▒▒▒▒▒▒┃"},
		{"fresh unused capacity", func(s *view.Snapshot) {
			s.Accounts[1].Windows[0].Used = 0
			s.Accounts[1].Windows[0].Forecast.Pct = 0
		}, "░░┃░░░░░░░░"},
		{"exhausted", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Used = 100 }, "██┃████████"},
		{"missing observation", func(s *view.Snapshot) { s.Accounts[1].Reading.ObservedAt = time.Time{} }, ""},
		{"future observation", func(s *view.Snapshot) { s.Accounts[1].Reading.ObservedAt = now.Add(time.Minute) }, ""},
		{"expired", func(s *view.Snapshot) { s.Accounts[1].Reading.State = view.ReadingExpired }, ""},
		{"error", func(s *view.Snapshot) { s.Accounts[1].Reading.State = view.ReadingError }, ""},
		{"unavailable", func(s *view.Snapshot) { s.Accounts[1].Reading.State = view.ReadingUnavailable }, ""},
		{"reset passed", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Reset.In = -time.Minute }, ""},
		{"non-finite use", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Used = math.NaN() }, ""},
		{"non-finite pace", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Pace = math.Inf(1) }, ""},
		{"non-finite projection", func(s *view.Snapshot) { s.Accounts[1].Windows[0].Forecast.Pct = math.NaN() }, ""},
		{"unused invalid pace", func(s *view.Snapshot) {
			s.Accounts[1].Windows[0].HasPace = false
			s.Accounts[1].Windows[0].Pace = math.NaN()
		}, "███▒▒▒▒▒▒▒"},
		{"withheld invalid projection", func(s *view.Snapshot) {
			s.Accounts[1].Windows[0].Forecast.State = view.ForecastStale
			s.Accounts[1].Windows[0].Forecast.Pct = math.Inf(1)
		}, "██┃█░░░░░░░"},
		{"no selected quota", func(s *view.Snapshot) { s.Selected = nil }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := snapshot()
			tc.tune(&snap)
			got := Tooltip(snap, nil)
			if tc.bar == "" {
				if strings.ContainsAny(got, "█░▒┃") {
					t.Fatalf("unusable reading drew a meter: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.bar) {
				t.Fatalf("hover = %q; want selected row bar %q", got, tc.bar)
			}
			window := snap.Accounts[1].Windows[0]
			if rowBar := meter.BarText(window.Row(false).Bar); tc.bar != rowBar {
				t.Fatalf("literal %q differs from canonical row %q", tc.bar, rowBar)
			}
			if snap.Accounts[1].Reading.State == view.ReadingStale {
				if !strings.Contains(got, "Last known 30% used  "+tc.bar) || strings.Index(got, "stale -") > strings.Index(got, tc.bar) {
					t.Fatalf("stale bar lost its qualification: %q", got)
				}
			}
		})
	}
}
