package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

// An expired credential with no quota windows is still the account the icon
// shows: the warning meter, not an empty one. It is what the tray drew before
// the snapshot, and an empty meter would hide the expiry.
func TestExpiredAccountWithNoWindowsDrawsTheWarningMeter(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{
		"claude": {Provider: "claude", IsExpired: true, FetchedAt: now.Add(-time.Hour)},
	}}, now)
	name, meter := IconState(snap)
	if name != "claude" || meter.UsagePct != 100 || meter.ExpectedPct != 100 || meter.RiskPct != 100 {
		t.Fatalf("IconState() = %s %+v; want claude with the warning meter", name, meter)
	}
}

// With an errored account and a healthy one, the icon names the healthy window,
// and an error with no windows draws no meter at all.
func TestErroredAccountWithNoWindowsDrawsNoMeter(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{
		"openai": {Provider: "openai", Error: "usage request failed (500)"},
	}}, now)
	name, meter := IconState(snap)
	if name != "openai" || meter.UsagePct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("IconState() = %s %+v; want openai with no meter", name, meter)
	}
}

func TestUpdateLineIsAddedOnlyForAVersion(t *testing.T) {
	if got := AppendUpdateLine("Claude: 5h", ""); got != "Claude: 5h" {
		t.Fatalf("AppendUpdateLine without a version = %q", got)
	}
	if got := AppendUpdateLine("Claude: 5h", "0.42.0"); got != "Claude: 5h\nUpdate available: 0.42.0" {
		t.Fatalf("AppendUpdateLine = %q", got)
	}
	if got := AppendUpdateLine("", "0.42.0"); !strings.HasPrefix(got, "Update available:") || strings.Contains(got, "\n") {
		t.Fatalf("AppendUpdateLine on an empty tooltip = %q", got)
	}
}

func TestMenuMoreCountsOnlyShownWindowsPastTheLimit(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var windows []provider.UsageWindow
	for i := 0; i < 10; i++ {
		windows = append(windows, provider.UsageWindow{Name: "w" + string(rune('a'+i)), Utilization: 50, ResetsAt: at.Add(time.Hour), Length: 5 * time.Hour})
	}
	windows = append(windows, provider.UsageWindow{Name: "idle", Utilization: 0, ResetsAt: at.Add(time.Hour), Length: 5 * time.Hour})
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"codex": {Provider: "openai", FetchedAt: at, Windows: windows}}}, at)
	if got := MenuMore(snap, 8)["codex"]; got != 2 {
		t.Fatalf("more = %d; want the two shown windows past eight rows, not the idle one", got)
	}
	if got := MenuMore(snap, 20)["codex"]; got != 0 {
		t.Fatalf("more = %d with room for every row", got)
	}
}
