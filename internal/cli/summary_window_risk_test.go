package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestSummaryRanksExhaustedUnknownAcrossCriticalProviders(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{Display: "Projected", Data: &provider.UsageData{Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 50, ResetsAt: now.Add(6 * 24 * time.Hour)},
		}}},
		{Display: "Exhausted", Data: &provider.UsageData{Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 100, ResetsAt: now.Add(299 * time.Minute)},
			{Name: "7d", Utilization: 10, ResetsAt: now.Add(time.Hour)},
		}}},
	}}
	got := captureStdout(t, func() { printSummary(output, false) })
	if !strings.Contains(got, "Exhausted 5h current 100% forecast soon") {
		t.Fatalf("summary = %q", got)
	}
	sortProvidersByUrgency(output.Providers)
	if output.Providers[0].Display != "Exhausted" {
		t.Fatalf("provider order = %+v", output.Providers)
	}
	quotas := output.buildView(now).agentQuotaSummaries()
	if len(quotas) != 3 || !strings.HasPrefix(quotas[0], "Exhausted 5h(") {
		t.Fatalf("agent quota order = %v", quotas)
	}
}

func TestSummaryUsesRetainedReadingForecast(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{2 * time.Minute, 11 * time.Minute} {
		output := &MultiProviderOutput{Providers: []ProviderFormatter{
			fmtr("claude", "Claude", "claude", &provider.UsageData{Stale: true, FetchedAt: now.Add(-age), Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 80, ResetsAt: now.Add(3 * time.Hour)}}}),
			fmtr("codex", "Codex", "openai", &provider.UsageData{Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 1, ResetsAt: now.Add(time.Hour)}}}),
		}}
		got := captureStdout(t, func() { output.buildView(now).printSummary(false) })
		if age <= 10*time.Minute {
			if !strings.Contains(got, "est. 203% at reset") || !strings.Contains(got, "runs out in 28m") || !strings.Contains(got, "last good") {
				t.Fatalf("young summary=%q", got)
			}
		} else if !strings.Contains(got, "forecast stale") || strings.Contains(got, "runs out") || strings.Contains(got, "out now") {
			t.Fatalf("old summary=%q", got)
		}
	}
}
