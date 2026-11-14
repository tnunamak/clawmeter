package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestSummaryRanksExhaustedUnknownAcrossCriticalProviders(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{Display: "Projected", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 50, ResetsAt: now.Add(6 * 24 * time.Hour)},
		}}},
		{Display: "Exhausted", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 100, ResetsAt: now.Add(299 * time.Minute)},
			{Name: "7d", Utilization: 10, ResetsAt: now.Add(time.Hour)},
		}}},
	}}
	got := captureStdout(t, func() { printSummary(output, false) })
	if !strings.Contains(got, "Exhausted 5h current 100% forecast soon") {
		t.Fatalf("summary = %q", got)
	}
	sortProvidersByUrgency(output)
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
	for _, tc := range []struct {
		age    time.Duration
		want   []string
		absent []string
	}{
		{2 * time.Minute, []string{"est. 203% at reset", "runs out in 28m", "last good"}, []string{"forecast stale"}},
		// Within the retained horizon the reading still forecasts, labelled with its age.
		{11 * time.Minute, []string{"est. 220% at reset", "runs out in 16m", "last good"}, []string{"forecast stale"}},
		{view.RetainedForecastAge + time.Minute, []string{"forecast stale"}, []string{"runs out", "out now"}},
	} {
		age := tc.age
		output := &MultiProviderOutput{Providers: []ProviderFormatter{
			fmtr("claude", "Claude", "claude", &provider.UsageData{Stale: true, FetchedAt: now.Add(-age), Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 80, ResetsAt: now.Add(3 * time.Hour)}}}),
			fmtr("codex", "Codex", "openai", &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 1, ResetsAt: now.Add(time.Hour)}}}),
		}}
		got := captureStdout(t, func() { output.buildView(now).printSummary(false) })
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Fatalf("age %v: summary=%q, missing %q", age, got, want)
			}
		}
		for _, absent := range tc.absent {
			if strings.Contains(got, absent) {
				t.Fatalf("age %v: summary=%q, has %q", age, got, absent)
			}
		}
	}
}
