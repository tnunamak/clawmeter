package forecast

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestCompareWindowRiskAccountsForCurrentExhaustion(t *testing.T) {
	known := Projection{ProjectedPct: 200, RunsOutIn: 1}
	for _, tc := range []struct {
		name    string
		current float64
		proj    Projection
		want    int
	}{
		{"unknown exhausted", 100, Projection{Unknown: true, WillLastToReset: true}, -1},
		{"unknown over quota", 110, Projection{Unknown: true, WillLastToReset: true}, -1},
		{"unknown below quota", 99, Projection{Unknown: true, WillLastToReset: true}, 1},
		{"stale exhausted", 100, Projection{Unknown: true, Stale: true, WillLastToReset: true}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := provider.UsageWindow{Utilization: tc.current}, provider.UsageWindow{Utilization: 50}
			if got := CompareWindowRisk(a, tc.proj, b, known); got != tc.want {
				t.Fatalf("CompareWindowRisk = %d, want %d", got, tc.want)
			}
			if got := CompareWindowRisk(b, known, a, tc.proj); got != -tc.want {
				t.Fatalf("reverse CompareWindowRisk = %d, want %d", got, -tc.want)
			}
		})
	}
}
