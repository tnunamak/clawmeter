//go:build tray

package tray

import (
	"context"
	"errors"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/update"
	"github.com/tnunamak/clawmeter/internal/view"
)

type sourceMenuTestProvider struct{ name, id, revision string }

func (p sourceMenuTestProvider) Name() string {
	if p.name == "" {
		return "claude"
	}
	return p.name
}
func (p sourceMenuTestProvider) DisplayName() string    { return "Claude" }
func (p sourceMenuTestProvider) Description() string    { return "" }
func (p sourceMenuTestProvider) DashboardURL() string   { return "" }
func (p sourceMenuTestProvider) IsConfigured() bool     { return true }
func (p sourceMenuTestProvider) SourceID() string       { return p.id }
func (p sourceMenuTestProvider) SourceLabel() string    { return p.id }
func (p sourceMenuTestProvider) SourceRevision() string { return p.revision }
func (p sourceMenuTestProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return nil, nil
}

func TestTrayCacheAndPriorResultsRequireMatchingSourceRevision(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", SourceID: "default", Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: time.Now().Add(time.Hour)}}}
	entry := &cache.Entry{
		ProviderData:    map[string]*provider.UsageData{"claude": data},
		SourceRevisions: map[string]string{"claude": "old-profile"},
	}

	if got, _ := cachedResultsForCurrentSources(entry, []provider.Provider{sourceMenuTestProvider{id: "default", revision: "new-profile"}}); len(got) != 0 {
		t.Fatalf("startup cache crossed profile revisions: %#v", got)
	}
	if got, _ := cachedResultsForCurrentSources(entry, []provider.Provider{sourceMenuTestProvider{id: "default"}}); len(got) != 0 {
		t.Fatalf("revisioned explicit cache survived switch to native default: %#v", got)
	}
	got, accepted := cachedResultsForCurrentSources(entry, []provider.Provider{sourceMenuTestProvider{id: "default", revision: "old-profile"}})
	if got["claude"] != data {
		t.Fatalf("matching source revision was not restored: %#v", got)
	}
	if accepted["claude"] != "old-profile" || len(accepted) != 1 {
		t.Fatalf("accepted revisions = %v; want the one the reading was accepted under", accepted)
	}

	prior := map[string]*provider.UsageData{"claude": data}
	if got := resultsMatchingSourceRevisions(prior, map[string]string{"claude": "old-profile"}, map[string]string{"claude": "new-profile"}); len(got) != 0 {
		t.Fatalf("refresh prior data crossed profile revisions: %#v", got)
	}
}

func TestTrayCacheRejectsDataFromAnotherSource(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", SourceID: "default", Windows: []provider.UsageWindow{{Name: "5h", Utilization: 80}}}
	entry := &cache.Entry{
		ProviderData:    map[string]*provider.UsageData{"claude:odl": data},
		SourceRevisions: map[string]string{"claude:odl": "same-revision"},
	}
	current := sourceMenuTestProvider{id: "odl", revision: "same-revision"}
	if got, _ := cachedResultsForCurrentSources(entry, []provider.Provider{current}); len(got) != 0 {
		t.Fatalf("startup cache relabeled another source: %#v", got)
	}
}

func TestApplyProviderEnablementUsesFamilyForEverySource(t *testing.T) {
	menus := map[string]*providerMenuItems{
		"claude":      {provider: sourceMenuTestProvider{id: "default"}},
		"claude:work": {provider: sourceMenuTestProvider{id: "work"}},
	}
	cfg := config.DefaultConfig()
	cfg.EnsureProvider("claude", true)
	applyProviderEnablement(menus, cfg)
	if !menus["claude"].explicitlyEnabled || !menus["claude:work"].explicitlyEnabled {
		t.Fatalf("family enablement did not reach every source: %#v", menus)
	}
	cfg.EnsureProvider("claude", false)
	applyProviderEnablement(menus, cfg)
	if menus["claude"].explicitlyEnabled || menus["claude:work"].explicitlyEnabled {
		t.Fatalf("family disablement did not reach every source: %#v", menus)
	}
}

func TestConfigureNotificationIdentity(t *testing.T) {
	old := beeep.AppName
	defer func() { beeep.AppName = old }()

	beeep.AppName = "DefaultAppName"
	configureNotificationIdentity()

	if beeep.AppName != "Clawmeter" {
		t.Fatalf("beeep.AppName = %q, want Clawmeter", beeep.AppName)
	}
}

func TestClaudeResetResultIsPublishedUnderTheMatchedSource(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"claude", "claude_web"},
		{"claude:odl", "claude_web:odl"},
	} {
		if got := claudeWebProviderKey(tc.target); got != tc.want {
			t.Errorf("claudeWebProviderKey(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestTrayDoesNotCreateMenusForLinkedClaudeResetSources(t *testing.T) {
	if shouldCreateProviderMenu(sourceMenuTestProvider{name: "claude_web", id: "default"}) {
		t.Fatal("linked Claude snapshot source should merge into its Claude account menu")
	}
	if !shouldCreateProviderMenu(sourceMenuTestProvider{name: "claude", id: "default"}) {
		t.Fatal("Claude account must keep its menu")
	}
}

func TestTrayProviderHeadingNamesAccountOnlyWhenRepeated(t *testing.T) {
	if got := trayProviderDisplayName("Claude", "Default", false); got != "Claude" {
		t.Fatalf("single default account heading = %q, want concise heading", got)
	}
	if got := trayProviderDisplayName("Claude", "ODL", true); got != "Claude · ODL" {
		t.Fatalf("multiple-account heading = %q", got)
	}
}

func TestResetSnapshotRowIsCompactAndVisibleWhenUsageIsStale(t *testing.T) {
	observedAt := time.Date(2026, time.September, 24, 17, 28, 0, 0, time.FixedZone("CDT", -5*60*60))
	data := &provider.UsageData{
		Provider: "claude",
		Stale:    true,
		ResetCredits: &provider.UsageResetCredits{
			AvailableCount:    1,
			EarliestExpiresAt: observedAt.Add(28 * 24 * time.Hour),
			FetchedAt:         observedAt,
			Snapshot:          true,
		},
	}
	got := resetCreditTraySummary(data, observedAt.Add(time.Hour))
	want := "Resets: 1 · expires " + observedAt.Add(28*24*time.Hour).Local().Format("Jan 2") + " · not updated since " + sinceLabel(observedAt, observedAt.Add(time.Hour)) + " · open claude.ai signed in to this account"
	if got != want {
		t.Errorf("reset snapshot row = %q, want %q", got, want)
	}
	if got := resetCreditTraySummary(&provider.UsageData{Stale: true, ResetCredits: &provider.UsageResetCredits{AvailableCount: 1}}, time.Now()); got != "" {
		t.Fatalf("stale non-snapshot reset inventory = %q, want hidden", got)
	}
}

func TestProviderConnectionMenuState(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		data         *provider.UsageData
		explicit     bool
		setupReady   bool
		wantStatus   string
		wantAction   string
		wantShow     bool
	}{
		{
			name:         "missing session",
			providerName: tokenPlanProviderName,
			explicit:     true,
			wantStatus:   "Quota access not connected",
			wantAction:   "Connect quota access",
			wantShow:     true,
		},
		{
			name:         "auto-detected setup without session",
			providerName: tokenPlanProviderName,
			setupReady:   false,
			wantStatus:   "Quota access not connected",
			wantAction:   "Connect quota access",
			wantShow:     true,
		},
		{
			name:         "no data with ready session is not a reconnect prompt",
			providerName: tokenPlanProviderName,
			explicit:     true,
			setupReady:   true,
			wantShow:     false,
		},
		{
			name:         "expired session",
			providerName: tokenPlanProviderName,
			data: &provider.UsageData{
				IsExpired: true,
				Error:     "Model Studio quota access expired",
			},
			wantStatus: "Quota access expired",
			wantAction: "Reconnect quota access",
			wantShow:   true,
		},
		{
			name:         "missing session returned as expired",
			providerName: tokenPlanProviderName,
			data: &provider.UsageData{
				IsExpired: true,
				Error:     "Model Studio quota access is not connected",
			},
			wantStatus: "Quota access not connected",
			wantAction: "Connect quota access",
			wantShow:   true,
		},
		{
			name:         "healthy provider",
			providerName: tokenPlanProviderName,
			data: &provider.UsageData{
				Windows: []provider.UsageWindow{{Name: "5h"}},
			},
			wantShow: false,
		},
		{
			name:         "other provider",
			providerName: "openai",
			data: &provider.UsageData{
				IsExpired: true,
			},
			wantShow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, action, show := providerConnectionMenuState(tt.providerName, tt.data, tt.explicit, tt.setupReady)
			if status != tt.wantStatus || action != tt.wantAction || show != tt.wantShow {
				t.Fatalf("providerConnectionMenuState() = (%q, %q, %v), want (%q, %q, %v)", status, action, show, tt.wantStatus, tt.wantAction, tt.wantShow)
			}
		})
	}
}

func TestProviderConnectFailureMessageIdentifiesMissingCLI(t *testing.T) {
	got := providerConnectFailureMessage("", "Alibaba's official Bailian CLI is not installed or is not on PATH")
	for _, want := range []string{"Bailian CLI is missing", "Node.js 22.12+", "npm install --global bailian-cli"} {
		if !strings.Contains(got, want) {
			t.Fatalf("providerConnectFailureMessage() = %q, missing %q", got, want)
		}
	}
}

func TestProviderConnectFailureMessageDoesNotEchoUnknownOutput(t *testing.T) {
	got := providerConnectFailureMessage("secret-looking output", "unexpected provider failure")
	if strings.Contains(got, "secret-looking") || strings.Contains(got, "unexpected provider failure") {
		t.Fatalf("providerConnectFailureMessage() echoed command output: %q", got)
	}
}

func TestTrayTitleShowsUpdateIndicator(t *testing.T) {
	oldRelease := currentPendingRelease()
	defer setPendingRelease(oldRelease)

	setPendingRelease(nil)
	if got := trayTitle(); got != trayTitleForPlatform(runtime.GOOS, false) {
		t.Fatalf("trayTitle without update = %q", got)
	}

	setPendingRelease(&update.Release{Version: "v9.9.9"})
	if got := trayTitle(); got != trayTitleForPlatform(runtime.GOOS, true) {
		t.Fatalf("trayTitle with update = %q", got)
	}
}

func TestTrayTitleForPlatform(t *testing.T) {
	for _, tc := range []struct {
		os     string
		update bool
		want   string
	}{
		{"darwin", false, ""},
		{"darwin", true, ""},
		{"linux", false, "Clawmeter"},
		{"linux", true, "Clawmeter •"},
		{"windows", false, "Clawmeter"},
		{"windows", true, "Clawmeter •"},
	} {
		if got := trayTitleForPlatform(tc.os, tc.update); got != tc.want {
			t.Errorf("trayTitleForPlatform(%q, %t) = %q, want %q", tc.os, tc.update, got, tc.want)
		}
	}
}

func TestTooltipShowsBlockedGap(t *testing.T) {
	// 99% used three hours before a reset runs out an hour and a half in,
	// well before the reset.
	window := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 99, ResetsAt: time.Now().Add(3 * time.Hour)}
	snap := viewOf(map[string]*provider.UsageData{"codex": {Windows: []provider.UsageWindow{window}}}, time.Now())
	got := Tooltip(snap, nil)

	for _, want := range []string{
		"Codex 7-Day",
		"Current 99%",
		"Runs out in ",
		"(",
		"before reset)",
		"Resets in",
		"Est. ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Tooltip() = %q, missing %q", got, want)
		}
	}
}

func TestTooltipAlreadyOutShowsWaitUntilReset(t *testing.T) {
	window := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 100, ResetsAt: time.Now().Add(2 * time.Hour)}
	snap := viewOf(map[string]*provider.UsageData{"codex": {Windows: []provider.UsageWindow{window}}}, time.Now())
	if got := Tooltip(snap, nil); !strings.Contains(got, "Out now (2h00m until reset)") {
		t.Fatalf("Tooltip() = %q, want already-out wait", got)
	}
}

func TestIconMeterStateUsesSoonestBlockingWindowForProviderDefault(t *testing.T) {
	now := time.Now()
	data := &provider.UsageData{
		Provider: "openai",
		Windows: []provider.UsageWindow{
			{
				Name:        "5h",
				Utilization: 90,
				ResetsAt:    now.Add(1 * time.Hour),
			},
			{
				Name:        "7d",
				Utilization: 50,
				ResetsAt:    now.Add(6 * 24 * time.Hour),
			},
		},
	}

	_, meter := IconState(viewOf(map[string]*provider.UsageData{"openai": data}, now))
	if !meter.ShowExpected {
		t.Fatal("meter should show expected pace for healthy usage windows")
	}
	if meter.Label != "5H" {
		t.Fatalf("meter.Label = %q, want 5H", meter.Label)
	}
	if meter.UsagePct != 90 {
		t.Fatalf("UsagePct = %.1f, want actual utilization from soonest blocking window", meter.UsagePct)
	}
	wantExpected := 80.0
	if absFloat(meter.ExpectedPct-wantExpected) > 0.5 {
		t.Fatalf("ExpectedPct = %.1f, want roughly %.1f", meter.ExpectedPct, wantExpected)
	}
	if meter.RiskPct < 110 || meter.RiskPct > 115 {
		t.Fatalf("RiskPct = %.1f, want projected overrun severity from rate", meter.RiskPct)
	}
}

func TestIconMeterStateKeepsUnavailableDataNeutral(t *testing.T) {
	_, meter := IconState(viewOf(map[string]*provider.UsageData{"claude": {
		Provider: "claude",
		Error:    "usage unavailable",
	}}, time.Now()))

	if meter.UsagePct != 0 || meter.ExpectedPct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want neutral provider icon without red failure gauge", meter)
	}
}

func TestIconMeterStateKeepsStaleDataNeutral(t *testing.T) {
	_, meter := IconState(viewOf(map[string]*provider.UsageData{"openai": {
		Provider: "openai",
		Stale:    true,
		Warning:  "failed to fetch codex rate limits",
		Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 99, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}}, time.Now()))

	if meter.UsagePct != 0 || meter.ExpectedPct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want neutral provider icon for stale data", meter)
	}
}

func TestWorstUsesTheSoonerRunOut(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
			},
		},
	}

	snap := viewOf(results, now)
	if snap.Worst == nil || snap.Worst.AccountKey != "claude" {
		t.Fatalf("worst = %+v, want claude because it runs out sooner", snap.Worst)
	}
}

func TestSelectedPrefersUsableQuotaOverErrorOnlyProvider(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Error:    "failed to fetch codex rate limits",
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(2 * time.Hour)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()

	snap := viewOf(results, now)
	if snap.Selected == nil || snap.Selected.AccountKey != "claude" || snap.Selected.Window != "5h" {
		t.Fatalf("selected = %+v, want claude/5h", snap.Selected)
	}
}

func TestIconProviderNameUsesProviderFamilyNotSourceKey(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", SourceID: "odl"}
	if got, _ := IconState(viewOf(map[string]*provider.UsageData{"claude:odl": data}, time.Now())); got != "claude" {
		t.Fatalf("IconState() provider = %q, want claude", got)
	}
}

func TestRiskOrderRanksEveryProviderWindowByUrgency(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
				{Name: "7d All", Utilization: 5, ResetsAt: now.Add(8 * time.Hour)},
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}

	got := riskOrderOf(viewOf(results, now))
	want := []iconTarget{
		{Provider: "claude", Window: "5h"},
		{Provider: "claude", Window: "7d Sonnet"},
		{Provider: "openai", Window: "7d"},
		{Provider: "openai", Window: "5h"},
		{Provider: "claude", Window: "7d All"},
	}
	if len(got) != len(want) {
		t.Fatalf("risk order len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("risk order[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestManualIconTargetsIncludeEveryWindowFromMultiSourceFamily(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude:a": {Provider: "claude", SourceID: "a", Windows: []provider.UsageWindow{{Name: "low", Utilization: 10, ResetsAt: now.Add(12 * time.Hour)}, {Name: "high", Utilization: 95, ResetsAt: now.Add(time.Hour)}}},
		"claude:b": {Provider: "claude", SourceID: "b", Windows: []provider.UsageWindow{{Name: "only", Utilization: 30, ResetsAt: now.Add(2 * time.Hour)}}},
	}
	got := manualIconTargets(results)
	if len(got) != 3 {
		t.Fatalf("targets = %+v, want every source window", got)
	}
	seen := map[string]bool{}
	for _, target := range got {
		seen[target.Provider+":"+target.Window] = true
	}
	if !seen["claude:a:low"] || !seen["claude:a:high"] || !seen["claude:b:only"] {
		t.Fatalf("targets = %+v, want every source window", got)
	}
}

func TestRiskPrefersSoonerRunoutOverHigherProjectedPct(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				// Projected ~115%, but not blocked until about 3d12h from now.
				{Name: "7d", Utilization: 42.3, ResetsAt: now.Add(4*24*time.Hour + 10*time.Hour + 29*time.Minute)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				// Projected ~106%, but blocked in about 1h35m from now.
				{Name: "7d", Utilization: 99.0, ResetsAt: now.Add(10*time.Hour + 58*time.Minute)},
			},
		},
	}

	got := riskOrderOf(viewOf(results, now))
	want := []iconTarget{
		{Provider: "claude", Window: "7d"},
		{Provider: "openai", Window: "7d"},
	}
	if len(got) != len(want) {
		t.Fatalf("risk order len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("risk order[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestManualIconTargetsIncludesFreshAndStaleWindows(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider:  "claude",
			Stale:     true,
			Warning:   "rate limited (429)",
			FetchedAt: now.Add(-15 * time.Minute),
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 95, ResetsAt: now.Add(1 * time.Hour)},
			},
		},
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 20, ResetsAt: now.Add(5 * 24 * time.Hour)},
			},
		},
	}

	got := manualIconTargets(results)
	want := map[iconTarget]bool{
		{Provider: "openai", Window: "7d"}: true,
		{Provider: "claude", Window: "5h"}: true,
	}
	if len(got) != len(want) {
		t.Fatalf("manualIconTargets = %+v, want fresh and stale targets", got)
	}
	for _, target := range got {
		if !want[target] {
			t.Fatalf("manualIconTargets includes unexpected target %+v", target)
		}
	}
}

func TestAutoSelectionStillPrefersFreshOverStale(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {Provider: "claude", Stale: true, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 95, ResetsAt: now.Add(time.Hour)}}},
		"openai": {Provider: "openai", Windows: []provider.UsageWindow{{Name: "7d", Utilization: 20, ResetsAt: now.Add(5 * 24 * time.Hour)}}},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()
	snap := viewOf(results, now)
	if snap.Selected == nil || snap.Selected.AccountKey != "openai" || snap.Selected.Window != "7d" {
		t.Fatalf("selected = %+v, want the fresh openai/7d", snap.Selected)
	}
}

func TestNoResetWindowIsSelectedOnlyWhenNothingElseIs(t *testing.T) {
	// Changed on purpose: the view ranks every window, reset or not, after the
	// fresh reset-backed ones. A used-up window with no reset is still usage now.
	results := map[string]*provider.UsageData{
		"balance-only":  {Balances: []provider.UsageBalance{{Name: "credits", Remaining: 1}}},
		"unknown-reset": {Windows: []provider.UsageWindow{{Name: "daily", Utilization: 99}}},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()
	snap := viewOf(results, time.Now())
	if snap.Selected == nil || snap.Selected.AccountKey != "unknown-reset" || snap.Selected.Window != "daily" {
		t.Fatalf("selected = %+v, want the only window", snap.Selected)
	}
	if _, meter := IconState(snap); meter.UsagePct != 99 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want 99%% used with no pace", meter)
	}
}

func TestStaleFallbackIsSelectedWhenNoFreshWindows(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider:  "claude",
			Stale:     true,
			Warning:   "rate limited (429)",
			FetchedAt: now.Add(-15 * time.Minute),
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 11, ResetsAt: now.Add(4 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()
	snap := viewOf(results, now)
	if snap.Selected == nil || snap.Selected.AccountKey != "claude" || snap.Selected.Window != "5h" {
		t.Fatalf("selected = %+v, want the stale claude/5h fallback", snap.Selected)
	}
}

func TestSelectedHonorsManualStaleOverride(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider: "claude",
			Stale:    true,
			Warning:  "rate limited (429)",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 95, ResetsAt: now.Add(1 * time.Hour)},
			},
		},
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 20, ResetsAt: now.Add(5 * 24 * time.Hour)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "claude", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	snap := viewOf(results, now)
	if snap.Selected == nil || snap.Selected.AccountKey != "claude" || snap.Selected.Window != "5h" || snap.Selected.Reason != "pinned" {
		t.Fatalf("selected = %+v, want the pinned stale claude/5h", snap.Selected)
	}
}

func TestBackedOffProviderWithoutPriorWindowsStillFetches(t *testing.T) {
	gate := provider.NewFailureGate()
	_ = gate.ShouldSurfaceError("openai", false)

	toFetch, skipped := splitProvidersForRefresh(
		[]provider.Provider{trayStubProvider{name: "openai"}},
		gate,
		map[string]*provider.UsageData{},
		false,
	)

	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if got := providerNames(toFetch); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("toFetch = %v, want [openai]", got)
	}
}

func TestBackedOffProviderWithPriorWindowsUsesClone(t *testing.T) {
	gate := provider.NewFailureGate()
	_ = gate.ShouldSurfaceError("openai", true)
	prev := &provider.UsageData{
		Provider: "openai",
		Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 25, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}

	toFetch, skipped := splitProvidersForRefresh(
		[]provider.Provider{trayStubProvider{name: "openai"}},
		gate,
		map[string]*provider.UsageData{"openai": prev},
		false,
	)

	if len(toFetch) != 0 {
		t.Fatalf("toFetch = %v, want none", providerNames(toFetch))
	}
	got := skipped["openai"]
	if got == nil {
		t.Fatal("skipped[openai] is nil, want cached usage")
	}
	if got == prev {
		t.Fatal("skipped data aliases prior result, want clone")
	}
	got.Windows[0].Utilization = 99
	if prev.Windows[0].Utilization != 25 {
		t.Fatalf("mutating skipped clone changed prior result to %.0f", prev.Windows[0].Utilization)
	}
}

func TestForceRefreshIgnoresBackoff(t *testing.T) {
	gate := provider.NewFailureGate()
	_ = gate.ShouldSurfaceError("openai", true)

	toFetch, skipped := splitProvidersForRefresh(
		[]provider.Provider{trayStubProvider{name: "openai"}},
		gate,
		map[string]*provider.UsageData{
			"openai": {
				Provider: "openai",
				Windows:  []provider.UsageWindow{{Name: "7d", ResetsAt: time.Now().Add(24 * time.Hour)}},
			},
		},
		true,
	)

	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if got := providerNames(toFetch); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("toFetch = %v, want [openai]", got)
	}
}

func TestSelectedHonorsQuotaOverride(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "claude", Window: "7d Sonnet"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	snap := viewOf(results, now)
	if snap.Selected == nil || snap.Selected.AccountKey != "claude" || snap.Selected.Window != "7d Sonnet" {
		t.Fatalf("selected = %+v, want the pinned claude/7d Sonnet", snap.Selected)
	}
}

func TestTrayTooltipDescribesCurrentAutoTarget(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()

	names := map[string]string{"claude": "Claude", "openai": "Codex"}
	got := Tooltip(viewNamed(results, names, now), results)

	// Changed on purpose: the used percentage is always stated, before the run-out.
	if !strings.HasPrefix(got, "Claude 7-Day Sonnet\nCurrent 90%\nRuns out in ") {
		t.Fatalf("Tooltip() = %q, want full title, used percentage, then run-out line", got)
	}
	if strings.Contains(got, "7S") || !strings.Contains(got, "Resets in") || !strings.Contains(got, "Est.") {
		t.Fatalf("Tooltip() = %q, want run-out, reset, and estimate", got)
	}
	if strings.Contains(got, " · ") || strings.Count(got, "\n") != 4 {
		t.Fatalf("Tooltip() = %q, want five newline-separated lines", got)
	}
}

func TestTrayTooltipDescribesPinnedTarget(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "7d Sonnet", Utilization: 90, ResetsAt: now.Add(6 * 24 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "openai", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	names := map[string]string{"claude": "Claude", "openai": "Codex"}
	got := Tooltip(viewNamed(results, names, now), results)

	// Changed on purpose: the used percentage is always stated, before the run-out.
	if !strings.HasPrefix(got, "Codex 5-Hour\nCurrent 20%\nWon't run out") {
		t.Fatalf("Tooltip() = %q, want full title, used percentage, then run-out state", got)
	}
	if strings.Contains(got, "5H") || !strings.Contains(got, "Resets in") || !strings.Contains(got, "Est.") {
		t.Fatalf("Tooltip() = %q, want run-out state, reset, and estimate", got)
	}
	if strings.Contains(got, " · ") || strings.Count(got, "\n") != 4 {
		t.Fatalf("Tooltip() = %q, want five newline-separated lines", got)
	}
}

func TestTrayTooltipIncludesResetCreditsForSelectedProvider(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 20, ResetsAt: now.Add(3 * time.Hour)},
			},
			ResetCredits: &provider.UsageResetCredits{
				AvailableCount: 2,
				Credits: []provider.UsageResetCredit{
					{Status: "available", ExpiresAt: now.Add(9 * 24 * time.Hour)},
				},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "openai", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	got := Tooltip(viewNamed(results, map[string]string{"openai": "Codex"}, now), results)

	if !strings.Contains(got, "\nResets: 2 · next expires ") {
		t.Fatalf("Tooltip() = %q, want reset-credit expiry line", got)
	}
	if strings.Count(got, "\n") != 5 {
		t.Fatalf("Tooltip() = %q, want six newline-separated lines with reset credits", got)
	}
}

func TestTrayTooltipShowsClaudeResetSnapshotWhenUsageIsErroredAndNeverNamesTheSupplementalSource(t *testing.T) {
	now := time.Now()
	for name, usage := range map[string]*provider.UsageData{
		"errored":      {Provider: "claude", SourceID: "default", Error: "usage request failed (500)"},
		"rate limited": {Provider: "claude", SourceID: "default", Error: "HTTP 429 rate limit exceeded"},
		"expired":      {Provider: "claude", SourceID: "default", IsExpired: true},
		"unavailable":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			raw := map[string]*provider.UsageData{"claude_web": {
				Provider: "claude_web", SourceID: "default", SourceLabel: "Default", ResetCreditsTarget: "claude", FetchedAt: now,
				ResetCredits: &provider.UsageResetCredits{Snapshot: true, AvailableCount: 1, FetchedAt: now, EarliestExpiresAt: now.Add(24 * time.Hour)},
			}}
			if usage != nil {
				raw["claude"] = usage
			}
			s.mu.Lock()
			s.iconTargetOverride = iconTarget{}
			s.mu.Unlock()
			presented := provider.PresentResetCreditSources(raw)
			// Only the menus' sources are shown, so claude_web is never named.
			got := Tooltip(viewNamed(presented, map[string]string{"claude": "Claude"}, now), presented)
			if !strings.HasPrefix(got, "Claude") || !strings.Contains(got, "\nResets: 1 · expires ") || strings.Contains(got, "claude_web") {
				t.Fatalf("Tooltip() = %q, want the Claude account with its reset observation only", got)
			}
		})
	}
}

func TestTrayTooltipDescribesStaleFallbackWithoutForecastingItAsLive(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {
			Provider:  "claude",
			Stale:     true,
			Warning:   "rate limited (429)",
			FetchedAt: now.Add(-15 * time.Minute),
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 11, ResetsAt: now.Add(4 * time.Hour)},
			},
		},
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()

	got := Tooltip(viewNamed(results, map[string]string{"claude": "Claude"}, now), results)

	// Changed on purpose: the stale window is named, with its freshness on the
	// line after its title. It is not forecast.
	if !strings.HasPrefix(got, "Claude 5-Hour\nstale - showing last good data from ") {
		t.Fatalf("Tooltip() = %q, want the window title then the stale line", got)
	}
	if !strings.Contains(got, "rate limited") || !strings.Contains(got, "Current 11%") {
		t.Fatalf("Tooltip() = %q, want the stale reason and the last used percentage", got)
	}
	if strings.Contains(got, "Won't run out") || strings.Contains(got, "Est.") || strings.Contains(got, "Runs out") {
		t.Fatalf("Tooltip() = %q, should not forecast stale data like live usage", got)
	}
}

func TestHumanWindowLabelUsesReadableQuotaNames(t *testing.T) {
	tests := []struct {
		name   string
		window provider.UsageWindow
		want   string
	}{
		{
			name:   "five hour",
			window: provider.UsageWindow{Name: "5h", DisplayName: "5 hours"},
			want:   "5-Hour",
		},
		{
			name:   "seven day openai",
			window: provider.UsageWindow{Name: "7d", DisplayName: "7 days"},
			want:   "7-Day",
		},
		{
			name:   "seven day sonnet",
			window: provider.UsageWindow{Name: "7d Sonnet", DisplayName: "7 days (Sonnet)"},
			want:   "7-Day Sonnet",
		},
		{
			name:   "provider display name fallback",
			window: provider.UsageWindow{Name: "monthly", DisplayName: "Monthly Credits"},
			want:   "Monthly Credits",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanWindowLabel(tt.window); got != tt.want {
				t.Fatalf("humanWindowLabel(%+v) = %q, want %q", tt.window, got, tt.want)
			}
		})
	}
}

func TestTrayWindowLabelPrefersCompactProviderLabel(t *testing.T) {
	tests := []struct {
		window provider.UsageWindow
		want   string
	}{
		{window: provider.UsageWindow{Name: "session_5h", DisplayName: "5h"}, want: "5h"},
		{window: provider.UsageWindow{Name: "weekly", DisplayName: "7d"}, want: "7d"},
		{window: provider.UsageWindow{Name: "monthly"}, want: "monthly"},
	}

	for _, tt := range tests {
		if got := meter.Label(tt.window); got != tt.want {
			t.Fatalf("meter.Label(%+v) = %q, want %q", tt.window, got, tt.want)
		}
	}
}

func TestWindowBadgeLabelUsesTwoCharacterQuotaCode(t *testing.T) {
	tests := map[string]string{
		"7d All":     "7A",
		"7d":         "7D",
		"5h":         "5H",
		"monthly":    "MO",
		"???":        "--",
		"7d Sonnet":  "7S",
		"daily-soft": "DA",
	}
	for input, want := range tests {
		if got := windowBadgeLabel(input); got != want {
			t.Fatalf("windowBadgeLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowBadgeLabelForWindowPrefersProviderDisplayCode(t *testing.T) {
	tests := []struct {
		window provider.UsageWindow
		want   string
	}{
		{window: provider.UsageWindow{Name: "session_5h", DisplayName: "5h"}, want: "5H"},
		{window: provider.UsageWindow{Name: "weekly", DisplayName: "7d"}, want: "7D"},
		{window: provider.UsageWindow{Name: "7d Sonnet"}, want: "7S"},
	}
	for _, tt := range tests {
		if got := windowBadgeLabel(meter.Label(tt.window)); got != tt.want {
			t.Fatalf("windowBadgeLabel(meter.Label(%+v)) = %q, want %q", tt.window, got, tt.want)
		}
	}
}

func TestExpectedUsagePctClampsToResetWindow(t *testing.T) {
	if got := meter.ExpectedPctAt(time.Now().Add(forecast.SevenDayWindow+time.Hour), forecast.SevenDayWindow, time.Now()); got != 0 {
		t.Fatalf("future-before-window expected usage = %.1f, want 0", got)
	}
	if got := meter.ExpectedPctAt(time.Now().Add(-time.Hour), forecast.SevenDayWindow, time.Now()); got != 100 {
		t.Fatalf("past-reset expected usage = %.1f, want 100", got)
	}
}

func absFloat(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}

type trayStubProvider struct {
	name string
}

func (p trayStubProvider) Name() string {
	return p.name
}

func (p trayStubProvider) DisplayName() string {
	return p.name
}

func (p trayStubProvider) Description() string {
	return ""
}

func (p trayStubProvider) DashboardURL() string {
	return ""
}

func (p trayStubProvider) IsConfigured() bool {
	return true
}

func (p trayStubProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return &provider.UsageData{Provider: p.name}, nil
}

func TestThresholdCheckPreservesRawFoldedResults(t *testing.T) {
	oldCfg := cfg
	cfg = config.DefaultConfig()
	defer func() { cfg = oldCfg; s.lastResults = nil }()
	raw := map[string]*provider.UsageData{
		"claude":     {Provider: "claude", Windows: []provider.UsageWindow{{Name: "5h", Utilization: 79, ResetsAt: time.Now().Add(time.Hour)}}},
		"claude_api": {Provider: "claude_api"},
	}
	s.lastResults = raw
	checkThresholds(map[string]*provider.UsageData{"claude": raw["claude"]}, nil)
	if s.lastResults["claude_api"] != raw["claude_api"] {
		t.Fatal("threshold rendering dropped raw claude_api fallback")
	}
}

func TestWindowBadgeDoesNotTruncateMultiDigitDuration(t *testing.T) {
	for _, name := range []string{"24 hours", "24h", "30 days", "30d"} {
		if got := windowBadgeLabel(name); got != "--" {
			t.Errorf("windowBadgeLabel(%q) = %q, want neutral -- rather than a truncated duration", name, got)
		}
	}
}

// Supplemental headless tests below require the new tray seams.
func TestThresholdCrossingsNotifyOnceOutsideStateLock(t *testing.T) {
	oldCfg, oldSend := cfg, sendThresholdNotification
	cfg = config.DefaultConfig()
	s.thresholdUtilization = nil
	defer func() {
		cfg, sendThresholdNotification = oldCfg, oldSend
		s.thresholdUtilization = nil
		s.lastResults = nil
	}()
	var urgencies []string
	sendThresholdNotification = func(title, body, urgency string) {
		if !s.mu.TryLock() {
			t.Fatal("notification sent while state mutex is held")
		}
		s.mu.Unlock()
		urgencies = append(urgencies, urgency)
	}
	reset := time.Now().Add(time.Hour)
	for _, pct := range []float64{79, 80, 80, 94, 95, 95} {
		data := &provider.UsageData{Provider: "claude", Windows: []provider.UsageWindow{{Name: "5h", Utilization: pct, ResetsAt: reset}}}
		// This is the order used by refresh: publish raw data before rendering.
		s.lastResults = map[string]*provider.UsageData{"claude": data, "claude_api": {Provider: "claude_api"}}
		checkThresholds(map[string]*provider.UsageData{"claude": data}, nil)
		if s.lastResults["claude_api"] == nil {
			t.Fatal("raw fallback was lost")
		}
	}
	if len(urgencies) != 2 || urgencies[0] != "normal" || urgencies[1] != "critical" {
		t.Fatalf("notifications = %v, want one warning and one critical", urgencies)
	}
}

func TestLatestTrayStateWaitsForRenderBarrier(t *testing.T) {
	old := map[string]*provider.UsageData{"old": {Provider: "old"}}
	fresh := map[string]*provider.UsageData{"fresh": {Provider: "fresh"}}
	s.lastResults = old
	defer func() { s.lastResults = nil }()
	started := make(chan struct{})
	release := make(chan struct{})
	barrier := trayTestRenderBarrier{started: started, release: release}
	done := make(chan struct{})
	var rendered map[string]*provider.UsageData
	go func() {
		rendered, _ = lockLatestTrayState(barrier)
		barrier.Unlock()
		close(done)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("render crossed the locked barrier")
	default:
	}
	// A usage refresh publishes while the older status render is queued.
	s.mu.Lock()
	s.lastResults = fresh
	s.mu.Unlock()
	close(release)
	<-done
	if rendered["fresh"] != fresh["fresh"] || rendered["old"] != nil {
		t.Fatalf("queued older render used stale snapshot: %v", rendered)
	}
}

func TestTrayConfigRetryRecoversAfterRepeatedLoadErrors(t *testing.T) {
	ticks := make(chan time.Time)
	quit := make(chan struct{})
	attempted := make(chan int)
	done := make(chan struct{})
	want := config.DefaultConfig()
	var got *config.Config
	attempts, stops := 0, 0
	go func() {
		retryTrayConfig(ticks, quit, func() (*config.Config, error) {
			attempts++
			attempted <- attempts
			if attempts < 3 {
				return nil, errors.New("invalid config")
			}
			return want, nil
		}, func(c *config.Config) { got = c }, func() { stops++ })
		close(done)
	}()
	for i := 1; i <= 3; i++ {
		ticks <- time.Now()
		if got := <-attempted; got != i {
			t.Fatalf("attempt = %d, want %d", got, i)
		}
	}
	<-done
	if got != want || stops != 0 {
		t.Fatalf("retry recovery: config=%p stops=%d", got, stops)
	}
}

func TestTrayConfigRetryQuitRemainsResponsive(t *testing.T) {
	quit := make(chan struct{})
	close(quit)
	stopped := false
	retryTrayConfig(make(chan time.Time), quit, func() (*config.Config, error) {
		t.Fatal("Quit should not wait for a config load")
		return nil, nil
	}, func(*config.Config) { t.Fatal("invalid config should not start providers") }, func() { stopped = true })
	if !stopped {
		t.Fatal("Quit was not handled")
	}
}

type trayTestRenderBarrier struct {
	started chan struct{}
	release chan struct{}
}

func (b trayTestRenderBarrier) Lock() {
	close(b.started)
	<-b.release
}

func (b trayTestRenderBarrier) Unlock() {}

func TestRedTeamThresholdHistoryRearmsForNewResetWindow(t *testing.T) {
	oldCfg, oldSend := cfg, sendThresholdNotification
	oldHistory := s.thresholdUtilization
	cfg = config.DefaultConfig()
	s.thresholdUtilization = nil
	defer func() {
		cfg, sendThresholdNotification = oldCfg, oldSend
		s.thresholdUtilization = oldHistory
	}()
	var urgencies []string
	sendThresholdNotification = func(_, _, urgency string) {
		if !s.mu.TryLock() {
			t.Fatal("notification sent with state lock held")
		}
		s.mu.Unlock()
		urgencies = append(urgencies, urgency)
	}
	reset := time.Now().Add(time.Hour)
	sample := func(pct float64, resetsAt time.Time) {
		checkThresholds(map[string]*provider.UsageData{
			"claude": {Provider: "claude", Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: pct, ResetsAt: resetsAt},
			}},
		}, nil)
	}
	sample(79, reset)
	sample(95, reset)
	// A sleeping machine can miss the low samples after reset. The first
	// observed sample in the next quota period must not inherit the old
	// period's already-critical notification history.
	sample(80, reset.Add(5*time.Hour))
	sample(80, reset.Add(5*time.Hour))
	if len(urgencies) != 2 || urgencies[0] != "critical" || urgencies[1] != "normal" {
		t.Fatalf("notifications across quota reset = %v, want [critical normal]", urgencies)
	}
}

func TestRedTeamConfigRetryHonorsQuitReceivedDuringSuccessfulLoad(t *testing.T) {
	ticks := make(chan time.Time, 1)
	quit := make(chan struct{})
	loading := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	ticks <- time.Now()
	readyCalls, stopCalls := 0, 0
	go func() {
		defer close(done)
		retryTrayConfig(ticks, quit, func() (*config.Config, error) {
			close(loading)
			<-release
			return config.DefaultConfig(), nil
		}, func(*config.Config) { readyCalls++ }, func() { stopCalls++ })
	}()
	<-loading
	// Quit is delivered before recovery completes. The error-menu Quit
	// item will be hidden on recovery, so its pending click cannot be
	// consumed by the new menu's event loop.
	close(quit)
	close(release)
	<-done
	if readyCalls != 0 || stopCalls != 1 {
		t.Fatalf("Quit during config load: ready=%d stop=%d, want ready=0 stop=1", readyCalls, stopCalls)
	}
}

func TestNewResetPeriodIgnoresJitter(t *testing.T) {
	now := time.Now()
	reset := now.Add(2 * time.Hour)
	for _, tc := range []struct {
		name              string
		previous, current time.Time
		want              bool
	}{
		{"first sample", time.Time{}, reset, true},
		{"same reset", reset, reset, false},
		{"jitter later", reset, reset.Add(3 * time.Second), false},
		{"jitter earlier", reset, reset.Add(-3 * time.Second), false},
		{"next period", reset, reset.Add(5 * time.Hour), true},
		{"previous reset passed", now.Add(-time.Minute), now.Add(5 * time.Hour), true},
	} {
		if got := newResetPeriod(tc.previous, tc.current, now); got != tc.want {
			t.Errorf("%s: newResetPeriod = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTrayTypedMonthlyPresentation(t *testing.T) {
	window := provider.UsageWindow{Name: "premium", Length: 30 * 24 * time.Hour, Utilization: 60, ResetsAt: time.Now().Add(12 * time.Hour)}
	data := &provider.UsageData{Windows: []provider.UsageWindow{window}}
	snap := viewOf(map[string]*provider.UsageData{"a": data}, time.Now())
	w := snap.Accounts[0].Windows[0]
	if w.Forecast.State != view.ForecastKnown || w.Forecast.Pct < 60 || w.Forecast.Pct > 62 {
		t.Fatalf("selected forecast = %+v", w.Forecast)
	}
	_, meter := IconState(snap)
	if meter.RiskPct < 60 || meter.RiskPct > 62 || meter.ExpectedPct < 98 || meter.ExpectedPct > 99 {
		t.Fatalf("monthly meter = %+v", meter)
	}
	if got := trayWindowText(window); !strings.HasSuffix(got, "\u200760% used    \u200761% at reset    resets in 12h    premium") {
		t.Fatalf("monthly menu = %q", got)
	}
}

func TestTrayUnknownAndStaleTooltipsShowCurrentUsage(t *testing.T) {
	now := time.Now()
	// A fresh window used up in warm-up has no forecast, but is out now.
	warming := provider.UsageWindow{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: now.Add(299 * time.Minute)}
	stale := &provider.UsageData{Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour), Windows: []provider.UsageWindow{warming}}
	for _, tc := range []struct {
		name string
		data *provider.UsageData
		want []string
		ban  []string
	}{
		{"unknown", &provider.UsageData{Windows: []provider.UsageWindow{warming}}, []string{"Current 100%", "Out now", "forecast soon"}, []string{"Won't run out", "Est. 0%"}},
		{"stale", stale, []string{"Current 100%", "Run-out unknown", "stale"}, []string{"Won't run out", "Est.", "Out now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Tooltip(viewNamed(map[string]*provider.UsageData{"t": tc.data}, map[string]string{"t": "Test"}, now), nil)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("tooltip = %q, missing %q", got, want)
				}
			}
			for _, banned := range tc.ban {
				if strings.Contains(got, banned) {
					t.Fatalf("tooltip = %q, must not contain %q", got, banned)
				}
			}
		})
	}
}

func TestTrayRiskSelectionPrefersExhaustedUnknownWindow(t *testing.T) {
	now := time.Now()
	exhausted := provider.UsageWindow{Name: "5h", Utilization: 100, ResetsAt: now.Add(299 * time.Minute)}
	projected := provider.UsageWindow{Name: "7d", Utilization: 50, ResetsAt: now.Add(6 * 24 * time.Hour)}
	data := &provider.UsageData{Windows: []provider.UsageWindow{projected, exhausted}}
	results := map[string]*provider.UsageData{
		"projected": {Windows: []provider.UsageWindow{projected}},
		"exhausted": data,
	}
	s.mu.Lock()
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()

	snap := viewOf(results, now)
	if snap.Selected == nil || snap.Selected.AccountKey != "exhausted" || snap.Selected.Window != exhausted.Name {
		t.Fatalf("selected = %+v, want the exhausted window", snap.Selected)
	}
	for _, w := range snap.Accounts[indexOfAccount(snap, "exhausted")].Windows {
		if w.Name == exhausted.Name && w.Forecast.State != view.ForecastWarming {
			t.Fatalf("exhausted forecast = %+v, want warming up", w.Forecast)
		}
	}
	if targets := riskOrderOf(snap); len(targets) != 3 || targets[0] != (iconTarget{Provider: "exhausted", Window: "5h"}) {
		t.Fatalf("risk order = %+v", targets)
	}
	if _, meter := IconState(snap); meter.RiskPct < 100 || meter.UsagePct != 100 {
		t.Fatalf("default exhausted meter = %+v", meter)
	}
}

func indexOfAccount(snap view.Snapshot, key string) int {
	for i, a := range snap.Accounts {
		if a.Key == key {
			return i
		}
	}
	return -1
}

func TestNewResetPeriodRequiresForwardMovement(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	for _, current := range []time.Time{past, past.Add(-time.Second), time.Time{}} {
		if newResetPeriod(past, current, now) {
			t.Fatalf("expired reset %v rearmed at %v", past, current)
		}
	}
	if newResetPeriod(time.Time{}, time.Time{}, now) {
		t.Fatal("unchanged missing reset rearmed")
	}
	if !newResetPeriod(past, past.Add(time.Second), now) {
		t.Fatal("later reset after previous reset passed must rearm")
	}
}

func TestThresholdsSkipExpiredSamplesWithoutLosingNewPeriod(t *testing.T) {
	oldCfg, oldSend, oldHistory := cfg, sendThresholdNotification, s.thresholdUtilization
	cfg = config.DefaultConfig()
	s.thresholdUtilization = nil
	t.Cleanup(func() { cfg, sendThresholdNotification, s.thresholdUtilization = oldCfg, oldSend, oldHistory })
	count := 0
	sendThresholdNotification = func(string, string, string) { count++ }
	sample := func(reset time.Time) {
		checkThresholds(map[string]*provider.UsageData{
			"claude": {Windows: []provider.UsageWindow{{Name: "5h", Utilization: 96, ResetsAt: reset}}},
		}, nil)
	}
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	sample(past)
	sample(past)
	if count != 0 || len(s.thresholdUtilization) != 0 {
		t.Fatalf("expired samples notified=%d history=%+v", count, s.thresholdUtilization)
	}
	sample(future)
	sample(past)
	sample(future)
	if count != 1 {
		t.Fatalf("same live period notified %d times", count)
	}
	sample(future.Add(5 * time.Hour))
	if count != 2 {
		t.Fatalf("new live period did not rearm: notifications=%d", count)
	}
}

func TestNextAPICreditDeadline(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	early := provider.UsageCreditPool{ObservationSource: "extension", ObservedAt: now.Add(-4 * time.Minute), Live: true}
	late := provider.UsageCreditPool{ObservationSource: "extension", ObservedAt: now, Live: true}
	stale := early
	stale.ObservedAt = now.Add(-6 * time.Minute)
	out := early
	out.SignedOut = true
	bookmark := early
	bookmark.ObservationSource = "bookmark"
	results := map[string]*provider.UsageData{"claude_api": {CreditPools: []provider.UsageCreditPool{late, stale, out, bookmark, early}}, "empty": nil}
	if got, want := nextAPICreditDeadline(results, now), now.Add(time.Minute+time.Second); !got.Equal(want) {
		t.Fatalf("deadline = %v, want %v", got, want)
	}
	if got := nextAPICreditDeadline(results, now.Add(time.Minute+time.Second)); !got.Equal(now.Add(5*time.Minute + time.Second)) {
		t.Fatalf("second deadline = %v", got)
	}
	if got := nextAPICreditDeadline(results, now.Add(6*time.Minute)); !got.IsZero() {
		t.Fatalf("stale deadline = %v", got)
	}
}

func TestAPICreditTimerUsesLatestPublishedState(t *testing.T) {
	oldResults := s.lastResults
	defer func() {
		trayRenderMu.Lock()
		defer trayRenderMu.Unlock()
		apiCreditsTimerGeneration++
		if apiCreditsTimer != nil {
			apiCreditsTimer.Stop()
		}
		s.mu.Lock()
		s.lastResults = oldResults
		s.mu.Unlock()
	}()
	now := time.Now()
	old := map[string]*provider.UsageData{"claude_api": {CreditPools: []provider.UsageCreditPool{{ObservationSource: "extension", ObservedAt: now.Add(-6 * time.Minute), Live: true}}}}
	fresh := map[string]*provider.UsageData{"claude_api": {CreditPools: []provider.UsageCreditPool{{ObservationSource: "extension", ObservedAt: now, Live: true}}}}
	trayRenderMu.Lock()
	// Schedule an already-due timer, then publish while its render is blocked.
	scheduleAPICreditRows(old, nil, now.Add(-2*time.Minute))
	generation := apiCreditsTimerGeneration
	s.mu.Lock()
	s.lastResults = fresh
	s.mu.Unlock()
	trayRenderMu.Unlock()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("credit timer failed to finish; possible render/state lock deadlock")
		case <-ticker.C:
			trayRenderMu.Lock()
			completed := apiCreditsTimerGeneration > generation
			pending := false
			if completed {
				pending = apiCreditsTimer.Stop()
			}
			trayRenderMu.Unlock()
			if completed {
				if !pending {
					t.Fatal("credit timer reused the old snapshot instead of scheduling the fresh reading's deadline")
				}
				return
			}
		}
	}
}

func TestLocalPublicationWaitsForRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	barrier := trayTestRenderBarrier{started: started, release: release}
	done := make(chan struct{})
	go func() {
		publishLocalResetSource(barrier, provider.NewRegistry(), "claude_api", nil, nil, nil, nil, nil)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("local publication bypassed the refresh lock; an in-flight refresh can overwrite extension data")
	case <-started:
	}
	select {
	case <-done:
		t.Fatal("local publication crossed the locked refresh barrier")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("local publication did not release the refresh barrier")
	}
}

func TestTrayRowLaysLabeledValuesOutInGutteredColumns(t *testing.T) {
	soon := provider.UsageWindow{Name: "five_hour", Length: 5 * time.Hour, Utilization: 7, ResetsAt: time.Now().Add(3*time.Hour + 30*time.Second)}
	if got := trayWindowText(soon); got != "5h    █▒░░┃░░░░░░    \u2007\u20077% used    \u200718% at reset    resets in 3h" || strings.Contains(got, "·") {
		t.Fatalf("row = %q", got)
	}
	policy := provider.UsageWindow{Name: "daily", Utilization: 40, ResetPolicy: "resets daily"}
	if got := trayWindowText(policy); !strings.HasSuffix(got, "    \u200740% used    \u2007\u2007no forecast    resets daily") {
		t.Fatalf("policy row = %q", got)
	}
	unknown := provider.UsageWindow{Name: "daily", Utilization: 40}
	if got := trayWindowText(unknown); !strings.HasSuffix(got, "    \u200740% used    \u2007\u2007no forecast    reset not reported") {
		t.Fatalf("unreported row = %q", got)
	}
}

func TestTrayWindowRowFlagsAWindowThatRunsOutBeforeReset(t *testing.T) {
	window := provider.UsageWindow{Name: "seven_day", Length: 7 * 24 * time.Hour, Utilization: 86, ResetsAt: time.Now().Add(34 * time.Hour)}
	got := trayWindowText(window)
	if !AtRisk(viewWindowOf(t, window)) || !strings.Contains(got, "    runs out in ") || strings.Contains(got, "before reset") {
		t.Fatalf("at-risk row = %q", got)
	}
	window.Utilization = 10
	if got := trayWindowText(window); AtRisk(viewWindowOf(t, window)) || strings.Contains(got, "runs out") {
		t.Fatalf("safe row = %q", got)
	}
	// Used up minutes into a window: no forecast yet, but already out.
	spent := provider.UsageWindow{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: time.Now().Add(299 * time.Minute)}
	if got := trayWindowText(spent); !AtRisk(viewWindowOf(t, spent)) || !strings.Contains(got, "forecast soon") || !strings.HasSuffix(got, "    out now") {
		t.Fatalf("exhausted row without a forecast = %q", got)
	}
}

func TestTrayWindowRowStatesForecastUseAtReset(t *testing.T) {
	// 72% used two days into a seven-day window projects 252%.
	window := provider.UsageWindow{Name: "seven_day", DisplayName: "7 days", Length: 7 * 24 * time.Hour, Utilization: 72, ResetsAt: time.Now().Add(5 * 24 * time.Hour)}
	if got := trayWindowText(window); !strings.HasPrefix(got, "7d    ███┃████▒▒▒    \u200772% used    252% at reset    resets in 5d    runs out in ") {
		t.Fatalf("over-limit row = %q", got)
	}
	window.Utilization = 10
	if got := trayWindowText(window); !strings.HasPrefix(got, "7d    █▒▒┃░░░░░░░    \u200710% used    \u200735% at reset    resets in 5d") {
		t.Fatalf("under-limit row = %q", got)
	}
}

func trayWindowText(window provider.UsageWindow) string {
	snap := viewOf(map[string]*provider.UsageData{"a": {Windows: []provider.UsageWindow{window}}}, time.Now())
	return MenuRows(snap, maxWindowItems)["a"][0]
}

func TestTrayForecastColumnWidensForAnEarlyForecastItShows(t *testing.T) {
	// 20% used two hours into a week projects about 1680%.
	early := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 20, ResetsAt: time.Now().Add(7*24*time.Hour - 2*time.Hour)}
	steady := provider.UsageWindow{Name: "5h", Length: 5 * time.Hour, Utilization: 40, ResetsAt: time.Now().Add(3 * time.Hour)}
	rows := MenuRows(viewOf(map[string]*provider.UsageData{
		"a": {Windows: []provider.UsageWindow{early}},
		"b": {Windows: []provider.UsageWindow{steady}},
	}, time.Now()), maxWindowItems)
	forecastEnd := func(row string) int { return len([]rune(row[:strings.Index(row, " at reset")])) }
	if forecastEnd(rows["a"][0]) != forecastEnd(rows["b"][0]) || !strings.Contains(rows["b"][0], "\u2007100% at reset") {
		t.Fatalf("forecasts do not line up:\n%q\n%q", rows["a"][0], rows["b"][0])
	}

	// A ninth window has no menu slot, so it must not widen the column.
	windows := make([]provider.UsageWindow, maxWindowItems)
	for i := range windows {
		windows[i] = steady
	}
	rows = MenuRows(viewOf(map[string]*provider.UsageData{"b": {Windows: append(windows, early)}}, time.Now()), maxWindowItems)
	if len(rows["b"]) != maxWindowItems || strings.Contains(rows["b"][0], "\u2007100% at reset") {
		t.Fatalf("a hidden window widened the column: %q", rows["b"])
	}
}

func TestTrayMeterMarksWhereAnEvenPaceWouldBe(t *testing.T) {
	// Two of seven days gone: an even pace has used 29%, so the mark sits
	// after the third cell. 72% used runs well past it.
	ahead := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 72, ResetsAt: time.Now().Add(5 * 24 * time.Hour)}
	if got := trayWindowText(ahead); !strings.HasPrefix(got, "7d    ███┃████▒▒▒    ") {
		t.Fatalf("ahead of pace = %q", got)
	}
	// A window with no reset has no pace; a figure space keeps the bar as
	// wide as the marked ones.
	policy := provider.UsageWindow{Name: "daily", Utilization: 40, ResetPolicy: "resets daily"}
	if got := trayWindowText(policy); !strings.Contains(got, "████░░░░░░\u2007    ") {
		t.Fatalf("no-reset row = %q", got)
	}
}

func TestTrayOffersTheBrowserExtensionUntilItIsSetUp(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		folder  bool
		contact time.Time
		want    bool
	}{
		{false, time.Time{}, true},
		{false, now.Add(-time.Hour), false},
		{false, now.Add(-8 * 24 * time.Hour), true},
		// Opening the setup once is enough; the menu does not ask again.
		{true, time.Time{}, false},
		{true, now.Add(-8 * 24 * time.Hour), false},
	} {
		if got := offerExtension(tc.folder, tc.contact, now); got != tc.want {
			t.Errorf("offerExtension(%v, %v) = %v, want %v", tc.folder, tc.contact, got, tc.want)
		}
	}
}

func TestIssueURLNamesTheBuildOnly(t *testing.T) {
	got := issueURL("v0.51.0", "linux/amd64", "KDE")
	parsed, err := url.Parse(got)
	if err != nil || parsed.Host != "github.com" || parsed.Path != "/tnunamak/clawmeter/issues/new" {
		t.Fatalf("issueURL = %q", got)
	}
	body := parsed.Query().Get("body")
	if !strings.Contains(body, "Clawmeter v0.51.0 · linux/amd64 · KDE") || !strings.Contains(body, "**What happened**") {
		t.Fatalf("body = %q", body)
	}
	if body := mustQuery(t, issueURL("v0.51.0", "darwin/arm64", "")); strings.HasSuffix(strings.TrimSpace(body), "· ") || !strings.HasSuffix(body, "darwin/arm64\n") {
		t.Fatalf("body without desktop = %q", body)
	}
}

func mustQuery(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get("body")
}

func TestThresholdNotificationsUseSharedForecastWords(t *testing.T) {
	oldCfg, oldSend := cfg, sendThresholdNotification
	cfg = config.DefaultConfig()
	defer func() { cfg, sendThresholdNotification = oldCfg, oldSend; s.thresholdUtilization = nil }()
	for _, tc := range []struct {
		name            string
		length, resetIn time.Duration
		want            string
	}{
		{"5h", 5 * time.Hour, 299 * time.Minute, "forecast soon"},
		{"custom", 0, time.Hour, "no forecast"},
	} {
		s.thresholdUtilization = nil
		var got string
		sendThresholdNotification = func(_, body, _ string) { got = body }
		checkThresholds(map[string]*provider.UsageData{"test": {Windows: []provider.UsageWindow{{Name: tc.name, Length: tc.length, Utilization: 80, ResetsAt: time.Now().Add(tc.resetIn)}}}}, nil)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("notification=%q, want %q", got, tc.want)
		}
	}
}
