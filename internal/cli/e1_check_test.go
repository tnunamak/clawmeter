package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
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
			output := &MultiProviderOutput{Providers: []ProviderFormatter{{Data: &provider.UsageData{FetchedAt: time.Now(), Windows: []provider.UsageWindow{w}}}}}
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
	data := &provider.UsageData{FetchedAt: time.Now(), Windows: []provider.UsageWindow{
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

// --check grades a reading kept after a failed poll as the display does: its
// forecast is projected from when it was taken, not recomputed at now.
func TestCheckUsesRetainedReadingForecast(t *testing.T) {
	now := time.Now()
	reading := func(age time.Duration) *MultiProviderOutput {
		data := &provider.UsageData{FetchedAt: now.Add(-age), Windows: []provider.UsageWindow{
			{Name: "5h", Length: 5 * time.Hour, Utilization: 39.5, ResetsAt: now.Add(3 * time.Hour)},
		}}
		data.MarkStale("rate limited (429)")
		return &MultiProviderOutput{Providers: []ProviderFormatter{{Display: "Test", Data: data}}}
	}
	// Two minutes old: projected at fetch time it runs out before the reset.
	young := reading(2 * time.Minute)
	if acct := young.buildView(now).snap.Accounts[0]; acct.Windows[0].Exhaustion.State != view.ExhaustionRunsOut {
		t.Fatalf("young reading exhaustion = %s, want runs_out", acct.Windows[0].Exhaustion.State)
	}
	if got := statusCheckExitCode(young); got != 2 {
		t.Fatalf("young retained reading exit=%d want 2", got)
	}
	// Older than two polls: no forecast, so stale is a warning.
	if got := statusCheckExitCode(reading(time.Hour)); got != 1 {
		t.Fatalf("old retained reading exit=%d want 1", got)
	}
}

// The summary names the window that makes an account critical.
func TestSummaryNamesExhaustedWindow(t *testing.T) {
	now := time.Now()
	data := &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{
		{Name: "5h", Length: 5 * time.Hour, Utilization: 10, ResetsAt: now.Add(time.Hour)},
		{Name: "mystery", Utilization: 100},
	}}
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{Display: "Test", Data: data}}}
	v := output.buildView(now)
	u := summaryUrgency(v.entries[0].pf, v.account(v.entries[0].key))
	if u.tier != 2 || u.window.Name != "mystery" {
		t.Fatalf("summary = tier %d window %q, want tier 2 window mystery", u.tier, u.window.Name)
	}
}
