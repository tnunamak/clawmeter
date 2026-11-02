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
	if strings.Contains(got, "Won't run out") || !strings.Contains(got, "At average pace since reset: lasts to reset") {
		t.Fatalf("unqualified forecast: %s", got)
	}
}
