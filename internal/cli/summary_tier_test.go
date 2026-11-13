package cli

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// Only a fresh reading at 100% is critical. A retained reading warns instead,
// and it stays healthy while its last known position is under 90% and no
// forecast has lapsed.
func TestSummaryTierAndExitForRetainedReadings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		age      time.Duration
		used     float64
		stale    bool
		wantTier int
		wantExit int
	}{
		{"fresh at the limit", 0, 100, false, 2, 2},
		{"fresh healthy", 0, 20, false, 4, 0},
		{"retained at the limit", 2 * time.Minute, 100, true, 3, 1},
		{"retained healthy within the horizon", 2 * time.Minute, 20, true, 4, 0},
		{"retained healthy past the horizon", time.Hour + time.Minute, 20, true, 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			data := &provider.UsageData{Provider: "claude", FetchedAt: now.Add(-tc.age), Windows: []provider.UsageWindow{
				{Name: "5h", Length: 5 * time.Hour, Utilization: tc.used, ResetsAt: now.Add(3 * time.Hour)},
			}}
			if tc.stale {
				data.MarkStale("rate limited (429)")
			}
			output := &MultiProviderOutput{Providers: []ProviderFormatter{{Display: "Test", Data: data}}}
			v := output.buildView(now)
			if got := v.summaryTier(v.entries[0].key); got != tc.wantTier {
				t.Fatalf("summary tier = %d, want %d", got, tc.wantTier)
			}
			if got := statusCheckExitCode(output); got != tc.wantExit {
				t.Fatalf("exit = %d, want %d", got, tc.wantExit)
			}
		})
	}
}
