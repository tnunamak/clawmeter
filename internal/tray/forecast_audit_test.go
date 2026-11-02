package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestAuditM4TooltipIdentifiesHistoricalAverage(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	data := map[string]*provider.UsageData{"a": {Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 30, ResetsAt: now.Add(108 * time.Hour)}}}}
	snap := view.Build(view.Inputs{Results: data, MaxReadingAge: time.Minute}, now)
	w := snap.Accounts[0].Windows[0]
	if w.Forecast.Pct != 84 {
		t.Fatalf("projection=%v want full-window average 84", w.Forecast.Pct)
	}
	got := Tooltip(snap, data)
	if strings.Contains(got, "Won't run out") || !strings.Contains(got, "Est. lasts to reset") || !strings.Contains(got, "avg. since reset") {
		t.Fatalf("unqualified forecast: %s", got)
	}
}

func TestTooltipKeepsCanonicalPaceAndProjection(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	window := view.Window{Name: "5h", Label: "5h", Used: 30, Pace: 40, HasPace: true,
		Forecast:   view.Forecast{State: view.ForecastKnown, Pct: 75},
		Reset:      view.Reset{Known: true, In: time.Hour},
		Exhaustion: view.Exhaustion{State: view.ExhaustionLasts},
	}
	snap := view.Snapshot{At: now, Selected: &view.Target{AccountKey: "work", Window: "5h", Reason: "pinned"}, Accounts: []view.Account{{
		Key: "work", Label: "Work", Reading: view.Reading{State: view.ReadingFresh, ObservedAt: now}, Windows: []view.Window{window}, Shown: []string{"5h"},
	}}}
	const bar = "███▒┃▒▒▒▒░░"
	tooltip := Tooltip(snap, nil)
	menu := strings.Join(MenuRows(snap, 8)["work"], "\n")
	for name, text := range map[string]string{"hover": tooltip, "menu": menu} {
		if !strings.Contains(text, bar) {
			t.Fatalf("%s omits usage/projected/even-pace semantics: %q", name, text)
		}
	}
	if !strings.Contains(tooltip, "75% at reset") || !strings.Contains(tooltip, "avg. since reset") {
		t.Fatalf("hover bar lacks the forecast's numeric amount and basis: %q", tooltip)
	}
}
