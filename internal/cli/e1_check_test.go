package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// This helper-level test requires statusCheckExitCode from the fix.
// The existing-entry-point regression above proves the classification on base.
func TestE1CheckExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name       string
		windowJSON string
		remaining  time.Duration
		want       int
	}{
		{"monthly", `{"name":"premium","utilization":60,"window_length_seconds":2592000}`, 12 * time.Hour, 0},
		{"early", `{"name":"5h","utilization":3}`, 298 * time.Minute, 0},
		{"expired", `{"name":"5h","utilization":100}`, -5 * time.Hour, 1},
		{"unknown reset", `{"name":"5h","utilization":100}`, 0, 2},
		{"unknown length", `{"name":"mystery","utilization":95}`, time.Hour, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var w provider.UsageWindow
			if err := json.Unmarshal([]byte(tt.windowJSON), &w); err != nil {
				t.Fatal(err)
			}
			if tt.remaining != 0 {
				w.ResetsAt = time.Now().Add(tt.remaining)
			}
			output := &MultiProviderOutput{Providers: []ProviderFormatter{{Data: &provider.UsageData{Windows: []provider.UsageWindow{w}}}}}
			if got := statusCheckExitCode(output); got != tt.want {
				t.Errorf("exit=%d want %d", got, tt.want)
			}
		})
	}
}

// An exhausted window whose length is unknown has no projection, but it is
// still the provider's worst window: the headline and agent output must not
// pick a healthy sibling while --check reports the provider critical.
func TestWorstWindowPrefersExhaustedOverHealthySibling(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	data := &provider.UsageData{Windows: []provider.UsageWindow{
		{Name: "mystery", Utilization: 100, ResetsAt: reset},
		{Name: "5h", Utilization: 10, ResetsAt: reset},
	}}
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{Display: "Test", Data: data}}}
	if got := statusCheckExitCode(output); got != 2 {
		t.Fatalf("exit=%d want 2", got)
	}
	snap := output.buildView(time.Now()).snap
	if snap.Worst == nil || snap.Worst.Window != "mystery" {
		t.Fatalf("worst window = %+v, want mystery", snap.Worst)
	}
	agent := output.AgentSummary()
	if !strings.Contains(agent, "worst=Test mystery;") || !strings.Contains(agent, "Test mystery(current=100%,projected_at_reset=unknown,reset_in=1h00m,status=at_risk,runs_out=now,runs_out_early_by_seconds=3600,runs_out_early_by=1h00m)") || strings.Contains(agent, "; status=unknown;") {
		t.Fatalf("agent summary = %q", agent)
	}
}
