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
	if !strings.Contains(got, "Exhausted 5h current 100% projection unknown") {
		t.Fatalf("summary = %q", got)
	}
	sortProvidersByUrgency(output.Providers)
	if output.Providers[0].Display != "Exhausted" {
		t.Fatalf("provider order = %+v", output.Providers)
	}
	quotas := output.agentQuotaSummaries()
	if len(quotas) != 3 || !strings.HasPrefix(quotas[0], "Exhausted 5h(") {
		t.Fatalf("agent quota order = %v", quotas)
	}
}
