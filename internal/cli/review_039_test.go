package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestReview039SummaryNamesExhaustedWindow(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{Display: "Test", Data: &provider.UsageData{Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 100, ResetsAt: time.Now().Add(299 * time.Minute)},
			{Name: "7d", Utilization: 10, ResetsAt: reset},
		}}},
		{Display: "Other", Data: &provider.UsageData{Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: 5, ResetsAt: reset},
		}}},
	}}
	if got := statusCheckExitCode(output); got != 2 {
		t.Fatalf("check=%d", got)
	}
	got := captureStdout(t, func() { printSummary(output, false) })
	if !strings.Contains(got, "Test 5h") {
		t.Fatalf("critical window hidden: summary=%q agent=%q", got, output.StatusLineSummary())
	}
}
