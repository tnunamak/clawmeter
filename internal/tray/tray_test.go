//go:build tray

package tray

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/update"
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

	if got := cachedResultsForCurrentSources(entry, []provider.Provider{sourceMenuTestProvider{id: "default", revision: "new-profile"}}); len(got) != 0 {
		t.Fatalf("startup cache crossed profile revisions: %#v", got)
	}
	if got := cachedResultsForCurrentSources(entry, []provider.Provider{sourceMenuTestProvider{id: "default"}}); len(got) != 0 {
		t.Fatalf("revisioned explicit cache survived switch to native default: %#v", got)
	}
	if got := cachedResultsForCurrentSources(entry, []provider.Provider{sourceMenuTestProvider{id: "default", revision: "old-profile"}}); got["claude"] != data {
		t.Fatalf("matching source revision was not restored: %#v", got)
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
	if got := cachedResultsForCurrentSources(entry, []provider.Provider{current}); len(got) != 0 {
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
	want := "Resets: 1 · expires " + observedAt.Add(28*24*time.Hour).Local().Format("Jan 2") + " · not updated since " + observedAt.Local().Format("15:04") + " · open claude.ai in your browser"
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

func TestCompactIconTooltipShowsBlockedGap(t *testing.T) {
	got := compactIconTooltip("Codex 7-Day", provider.UsageWindow{
		Name:     "7d",
		ResetsAt: time.Now().Add(3 * time.Hour),
	}, forecast.Projection{
		ProjectedPct:    124,
		WillLastToReset: false,
		RunsOutIn:       90 * time.Minute,
		RunsOutEarlyBy:  time.Hour,
	})

	for _, want := range []string{
		"Codex 7-Day",
		"Runs out in 1h30m (1h00m before reset)",
		"Resets in",
		"Est. 124% at reset",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compactIconTooltip() = %q, missing %q", got, want)
		}
	}
}

func TestCompactIconTooltipAlreadyOutShowsWaitUntilReset(t *testing.T) {
	got := compactIconTooltip("Codex 7-Day", provider.UsageWindow{
		Name:     "7d",
		ResetsAt: time.Now().Add(2 * time.Hour),
	}, forecast.Projection{
		ProjectedPct:    180,
		WillLastToReset: false,
		RunsOutEarlyBy:  2 * time.Hour,
	})

	if !strings.Contains(got, "Out now (2h00m until reset)") {
		t.Fatalf("compactIconTooltip() = %q, want already-out wait", got)
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

	meter := iconMeterState(data, "")
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
	meter := iconMeterState(&provider.UsageData{
		Provider: "claude",
		Error:    "usage unavailable",
	}, "")

	if meter.UsagePct != 0 || meter.ExpectedPct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want neutral provider icon without red failure gauge", meter)
	}
}

func TestIconMeterStateKeepsStaleDataNeutral(t *testing.T) {
	meter := iconMeterState(&provider.UsageData{
		Provider: "openai",
		Stale:    true,
		Warning:  "failed to fetch codex rate limits",
		Windows: []provider.UsageWindow{
			{Name: "7d", Utilization: 99, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}, "")

	if meter.UsagePct != 0 || meter.ExpectedPct != 0 || meter.RiskPct != 0 || meter.ShowExpected {
		t.Fatalf("meter = %+v, want neutral provider icon for stale data", meter)
	}
}

func TestProviderSeverityUsesRiskUrgency(t *testing.T) {
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

	keys := sortedKeys(results)
	if len(keys) == 0 {
		t.Fatal("sortedKeys() returned no providers")
	}
	if keys[0] != "claude" {
		t.Fatalf("sortedKeys()[0] = %q, want claude because it runs out sooner; keys=%v", keys[0], keys)
	}
}

func TestSelectedTrayTargetPrefersUsableQuotaOverErrorOnlyProvider(t *testing.T) {
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

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "claude" || windowName != "5h" {
		t.Fatalf("selected target = %s/%s, want claude/5h", name, windowName)
	}
}

func TestIconProviderNameUsesProviderFamilyNotSourceKey(t *testing.T) {
	data := &provider.UsageData{Provider: "claude", SourceID: "odl"}
	if got := iconProviderName(data); got != "claude" {
		t.Fatalf("iconProviderName() = %q, want claude", got)
	}
}

func TestActiveIconTargetsOrdersEveryProviderWindowByRiskUrgency(t *testing.T) {
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

	got := activeIconTargets(results)
	want := []iconTarget{
		{Provider: "claude", Window: "5h"},
		{Provider: "claude", Window: "7d Sonnet"},
		{Provider: "openai", Window: "7d"},
		{Provider: "openai", Window: "5h"},
		{Provider: "claude", Window: "7d All"},
	}
	if len(got) != len(want) {
		t.Fatalf("activeIconTargets len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeIconTargets[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
		}
	}
}

func TestActiveIconTargetsIncludesEveryWindowFromMultiSourceFamily(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude:a": {Provider: "claude", SourceID: "a", Windows: []provider.UsageWindow{{Name: "low", Utilization: 10, ResetsAt: now.Add(12 * time.Hour)}, {Name: "high", Utilization: 95, ResetsAt: now.Add(time.Hour)}}},
		"claude:b": {Provider: "claude", SourceID: "b", Windows: []provider.UsageWindow{{Name: "only", Utilization: 30, ResetsAt: now.Add(2 * time.Hour)}}},
	}
	got := activeIconTargets(results)
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

func TestActiveIconTargetsRiskPrefersSoonerRunoutOverHigherProjectedPct(t *testing.T) {
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

	got := activeIconTargets(results)
	want := []iconTarget{
		{Provider: "claude", Window: "7d"},
		{Provider: "openai", Window: "7d"},
	}
	if len(got) != len(want) {
		t.Fatalf("activeIconTargets len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeIconTargets[%d] = %+v, want %+v; got=%+v", i, got[i], want[i], got)
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

func TestActiveIconTargetsStillPrefersFreshForAutoSelection(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {Provider: "claude", Stale: true, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 95, ResetsAt: now.Add(time.Hour)}}},
		"openai": {Provider: "openai", Windows: []provider.UsageWindow{{Name: "7d", Utilization: 20, ResetsAt: now.Add(5 * 24 * time.Hour)}}},
	}
	got := activeIconTargets(results)
	want := iconTarget{Provider: "openai", Window: "7d"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("activeIconTargets = %+v, want only fresh auto target %+v", got, want)
	}
}

func TestActiveIconTargetsExcludesNonForecastableFacts(t *testing.T) {
	results := map[string]*provider.UsageData{
		"balance-only":  {Balances: []provider.UsageBalance{{Name: "credits", Remaining: 1}}},
		"unknown-reset": {Windows: []provider.UsageWindow{{Name: "daily", Utilization: 99}}},
	}
	if got := activeIconTargets(results); len(got) != 0 {
		t.Fatalf("activeIconTargets() = %+v, want no non-forecastable targets", got)
	}
}

func TestActiveIconTargetsKeepsStaleFallbackWhenNoFreshWindows(t *testing.T) {
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

	got := activeIconTargets(results)
	want := []iconTarget{{Provider: "claude", Window: "5h"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("activeIconTargets = %+v, want stale fallback target %+v", got, want)
	}
}

func TestSelectedTrayTargetHonorsManualStaleOverride(t *testing.T) {
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

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "claude" || windowName != "5h" {
		t.Fatalf("selected target = %s/%s, want manually selected stale claude/5h", name, windowName)
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

func TestSelectedTrayTargetHonorsQuotaOverride(t *testing.T) {
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

	s.mu.Lock()
	s.iconTargetOverride = iconTarget{Provider: "claude", Window: "5h"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconTargetOverride = iconTarget{}
		s.mu.Unlock()
	}()

	name, _, windowName, ok := selectedTrayTarget(results)
	if !ok {
		t.Fatal("selectedTrayTarget returned no provider")
	}
	if name != "claude" || windowName != "5h" {
		t.Fatalf("selected target = %s/%s, want claude/5h", name, windowName)
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

	got := trayTooltip(results, map[string]string{"claude": "Claude", "openai": "Codex"})

	if !strings.HasPrefix(got, "Claude 7-Day Sonnet\nRuns out in ") {
		t.Fatalf("trayTooltip() = %q, want full title followed by run-out line", got)
	}
	if strings.Contains(got, "7S") || !strings.Contains(got, "Resets in") || !strings.Contains(got, "Est.") {
		t.Fatalf("trayTooltip() = %q, want run-out, reset, and estimate", got)
	}
	if strings.Contains(got, " · ") || strings.Count(got, "\n") != 3 {
		t.Fatalf("trayTooltip() = %q, want four newline-separated lines", got)
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

	got := trayTooltip(results, map[string]string{"claude": "Claude", "openai": "Codex"})

	if !strings.HasPrefix(got, "Codex 5-Hour\nWon't run out") {
		t.Fatalf("trayTooltip() = %q, want full title followed by run-out state", got)
	}
	if strings.Contains(got, "5H") || !strings.Contains(got, "Resets in") || !strings.Contains(got, "Est.") {
		t.Fatalf("trayTooltip() = %q, want run-out state, reset, and estimate", got)
	}
	if strings.Contains(got, " · ") || strings.Count(got, "\n") != 3 {
		t.Fatalf("trayTooltip() = %q, want four newline-separated lines", got)
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

	got := trayTooltip(results, map[string]string{"openai": "Codex"})

	if !strings.Contains(got, "\nResets: 2 · next expires ") {
		t.Fatalf("trayTooltip() = %q, want reset-credit expiry line", got)
	}
	if strings.Count(got, "\n") != 4 {
		t.Fatalf("trayTooltip() = %q, want five newline-separated lines with reset credits", got)
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
			got := trayTooltip(provider.PresentResetCreditSources(raw), map[string]string{"claude": "Claude"})
			if !strings.HasPrefix(got, "Claude") || !strings.Contains(got, "\nResets: 1 · expires ") || strings.Contains(got, "claude_web") {
				t.Fatalf("trayTooltip() = %q, want the Claude account with its reset observation only", got)
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

	got := trayTooltip(results, map[string]string{"claude": "Claude"})

	if !strings.HasPrefix(got, "Claude: stale - showing last good data from ") {
		t.Fatalf("trayTooltip() = %q, want stale fallback summary", got)
	}
	if !strings.Contains(got, "rate limited") {
		t.Fatalf("trayTooltip() = %q, want stale reason", got)
	}
	if strings.Contains(got, "Won't run out") || strings.Contains(got, "Est.") {
		t.Fatalf("trayTooltip() = %q, should not forecast stale data like live usage", got)
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
		if got := trayWindowLabel(tt.window); got != tt.want {
			t.Fatalf("trayWindowLabel(%+v) = %q, want %q", tt.window, got, tt.want)
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
		if got := windowBadgeLabelForWindow(tt.window); got != tt.want {
			t.Fatalf("windowBadgeLabelForWindow(%+v) = %q, want %q", tt.window, got, tt.want)
		}
	}
}

func TestExpectedUsagePctClampsToResetWindow(t *testing.T) {
	if got := expectedUsagePct(time.Now().Add(forecast.SevenDayWindow+time.Hour), forecast.SevenDayWindow); got != 0 {
		t.Fatalf("future-before-window expected usage = %.1f, want 0", got)
	}
	if got := expectedUsagePct(time.Now().Add(-time.Hour), forecast.SevenDayWindow); got != 100 {
		t.Fatalf("past-reset expected usage = %.1f, want 100", got)
	}
}

func TestPollIntervalForConfigEnforcesFiveMinuteFloor(t *testing.T) {
	tests := map[int]time.Duration{
		0:   5 * time.Minute,
		60:  5 * time.Minute,
		299: 5 * time.Minute,
		300: 5 * time.Minute,
		600: 10 * time.Minute,
	}
	for configured, want := range tests {
		if got := pollIntervalForConfig(configured); got != want {
			t.Fatalf("pollIntervalForConfig(%d) = %s, want %s", configured, got, want)
		}
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
	_, proj, ok := selectedIconWindow(data, window.Name)
	if !ok || proj.Unknown || proj.ProjectedPct < 60 || proj.ProjectedPct > 62 {
		t.Fatalf("selected projection = %+v, ok=%t", proj, ok)
	}
	meter := iconMeterState(data, window.Name)
	if meter.RiskPct < 60 || meter.RiskPct > 62 || meter.ExpectedPct < 98 || meter.ExpectedPct > 99 {
		t.Fatalf("monthly meter = %+v", meter)
	}
	if got := trayWindowStatus(window, windowProjection(window), false, defaultMarginWidth); !strings.Contains(got, "−39% at reset  ·  \u200760% now  ·  resets in 12h00m  ·  premium") {
		t.Fatalf("monthly menu = %q", got)
	}
}

func TestTrayUnknownAndStaleTooltipsShowCurrentUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		proj forecast.Projection
		want string
	}{
		{"unknown", forecast.Projection{Unknown: true, WillLastToReset: true}, "projection unknown"},
		{"stale", forecast.Projection{Unknown: true, Stale: true, WillLastToReset: true}, "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window := provider.UsageWindow{Utilization: 100, ResetsAt: time.Now().Add(time.Hour)}
			got := compactIconTooltip("Test", window, tc.proj)
			if !strings.Contains(got, "Current 100%") || !strings.Contains(got, tc.want) || strings.Contains(got, "Won't run out") || strings.Contains(got, "Est. 0%") {
				t.Fatalf("tooltip = %q", got)
			}
		})
	}
}

func TestTrayRiskSelectionPrefersExhaustedUnknownWindow(t *testing.T) {
	now := time.Now()
	exhausted := provider.UsageWindow{Name: "5h", Utilization: 100, ResetsAt: now.Add(299 * time.Minute)}
	projected := provider.UsageWindow{Name: "7d", Utilization: 50, ResetsAt: now.Add(6 * 24 * time.Hour)}
	data := &provider.UsageData{Windows: []provider.UsageWindow{projected, exhausted}}
	window, proj, ok := selectedIconWindow(data, "")
	if !ok || window.Name != exhausted.Name || !proj.Unknown {
		t.Fatalf("selected %q, projection=%+v, ok=%t", window.Name, proj, ok)
	}
	results := map[string]*provider.UsageData{
		"projected": {Windows: []provider.UsageWindow{projected}},
		"exhausted": data,
	}
	targets := activeIconTargets(results)
	if len(targets) != 3 || targets[0] != (iconTarget{Provider: "exhausted", Window: "5h"}) {
		t.Fatalf("risk targets = %+v", targets)
	}
	if keys := sortedKeys(results); len(keys) != 2 || keys[0] != "exhausted" {
		t.Fatalf("provider order = %v", keys)
	}
	if meter := iconMeterState(data, ""); meter.RiskPct < 100 || meter.UsagePct != 100 {
		t.Fatalf("default exhausted meter = %+v", meter)
	}
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

func TestTrayWindowRowsShareOneSeparatorStyle(t *testing.T) {
	soon := provider.UsageWindow{Name: "five_hour", Length: 5 * time.Hour, Utilization: 7, ResetsAt: time.Now().Add(3*time.Hour + 30*time.Second)}
	if got := trayWindowStatus(soon, windowProjection(soon), false, defaultMarginWidth); got != "5H   █▒░░░░░░░░   \u2007−82% at reset  ·  \u2007\u20077% now  ·  resets in \u20073h00m" || strings.Contains(got, "—") {
		t.Fatalf("row = %q", got)
	}
	policy := provider.UsageWindow{Name: "daily", Utilization: 40, ResetPolicy: "resets daily"}
	if got := trayWindowStatus(policy, windowProjection(policy), false, defaultMarginWidth); !strings.HasSuffix(got, "   \u200740% now  ·  resets daily") {
		t.Fatalf("policy row = %q", got)
	}
	unknown := provider.UsageWindow{Name: "daily", Utilization: 40}
	if got := trayWindowStatus(unknown, windowProjection(unknown), false, defaultMarginWidth); !strings.HasSuffix(got, "   \u200740% now  ·  reset not reported") {
		t.Fatalf("unreported row = %q", got)
	}
}

func TestTrayWindowRowFlagsAWindowThatRunsOutBeforeReset(t *testing.T) {
	window := provider.UsageWindow{Name: "seven_day", Length: 7 * 24 * time.Hour, Utilization: 86, ResetsAt: time.Now().Add(34 * time.Hour)}
	got := trayWindowStatus(window, windowProjection(window), false, defaultMarginWidth)
	if !windowRunsOutBeforeReset(window) || strings.HasPrefix(got, "⚠") || !strings.Contains(got, "  ·  runs out in ") || strings.Contains(got, "before reset") {
		t.Fatalf("at-risk row = %q", got)
	}
	window.Utilization = 10
	if got := trayWindowStatus(window, windowProjection(window), false, defaultMarginWidth); windowRunsOutBeforeReset(window) || strings.Contains(got, "runs out") {
		t.Fatalf("safe row = %q", got)
	}
}

func TestMenuWindowsDropIdleWindowsButNeverAll(t *testing.T) {
	soon := time.Now().Add(24 * time.Hour)
	used := provider.UsageWindow{Name: "seven_day", Length: 7 * 24 * time.Hour, Utilization: 40, ResetsAt: soon}
	idle := provider.UsageWindow{Name: "seven_day_fable", Length: 7 * 24 * time.Hour, Utilization: 0, ResetsAt: soon}
	if got := menuWindows([]provider.UsageWindow{used, idle}); len(got) != 1 || got[0].Name != used.Name {
		t.Fatalf("windows = %+v", got)
	}
	if got := menuWindows([]provider.UsageWindow{idle}); len(got) != 1 {
		t.Fatalf("an all-idle provider lost its rows: %+v", got)
	}
}

func TestTrayWindowRowLeadsWithAFixedScaleMeterAndStatesOvershoot(t *testing.T) {
	for _, tc := range []struct {
		used, projected float64
		bar             string
	}{
		{42, 80, "████▒▒▒▒░░"},
		{61, 104, "██████▒▒▒▒"},
		{68, 213, "███████▒▒▒"},
		{0, 0, "░░░░░░░░░░"},
		{120, 300, "██████████"},
		{50, 40, "█████░░░░░"},
		{math.NaN(), math.NaN(), "░░░░░░░░░░"},
		{40, math.NaN(), "████░░░░░░"},
	} {
		if got := meterBar(tc.used, tc.projected); got != tc.bar {
			t.Errorf("meterBar(%v, %v) = %q, want %q", tc.used, tc.projected, got, tc.bar)
		}
	}
	// 72% used two days into a seven-day window projects 252%.
	window := provider.UsageWindow{Name: "seven_day", DisplayName: "7 days", Length: 7 * 24 * time.Hour, Utilization: 72, ResetsAt: time.Now().Add(5 * 24 * time.Hour)}
	got := trayWindowStatus(window, windowProjection(window), false, defaultMarginWidth)
	if !strings.HasPrefix(got, "7D   ███████▒▒▒   +152% at reset  ·  \u200772% now  ·  resets in \u20075d\u20070h  ·  runs out in ") {
		t.Fatalf("over-limit row = %q", got)
	}
	// Half a five-hour window gone at 50.2% projects 100.4%: still over.
	nearly := provider.UsageWindow{Name: "five_hour", Length: 5 * time.Hour, Utilization: 50.2, ResetsAt: time.Now().Add(150 * time.Minute)}
	if got := trayWindowStatus(nearly, windowProjection(nearly), false, defaultMarginWidth); !strings.Contains(got, "+<1% at reset") {
		t.Fatalf("barely-over row = %q", got)
	}
	nearly.Utilization = 49.8
	if got := trayWindowStatus(nearly, windowProjection(nearly), false, defaultMarginWidth); !strings.Contains(got, "−<1% at reset") {
		t.Fatalf("barely-under row = %q", got)
	}
	window.Utilization = 10
	if got := trayWindowStatus(window, windowProjection(window), false, defaultMarginWidth); !strings.HasPrefix(got, "7D   █▒▒░░░░░░░   \u2007−65% at reset  ·  \u200710% now") {
		t.Fatalf("under-limit row = %q", got)
	}
}

func testWindowRows(windows ...provider.UsageWindow) []string {
	projections := make([]forecast.Projection, len(windows))
	for i, window := range windows {
		projections[i] = windowProjection(window)
	}
	return trayWindowRows(windows, projections, defaultMarginWidth)
}

func TestTrayMarginColumnWidensForAnEarlyForecastItShows(t *testing.T) {
	early := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 20, ResetsAt: time.Now().Add(7*24*time.Hour - 2*time.Hour)}
	steady := provider.UsageWindow{Name: "5h", Length: 5 * time.Hour, Utilization: 40, ResetsAt: time.Now().Add(3 * time.Hour)}
	menus := map[string]*providerMenuItems{
		"a": {windowItems: make([]*systray.MenuItem, maxWindowItems)},
		"b": {windowItems: make([]*systray.MenuItem, maxWindowItems)},
	}
	rows := trayMenuRows(map[string]*provider.UsageData{
		"a": {Windows: []provider.UsageWindow{early}},
		"b": {Windows: []provider.UsageWindow{steady}},
	}, menus)
	marginEnd := func(row string) int { return len([]rune(row[:strings.Index(row, " at reset")])) }
	if marginEnd(rows["a"][0]) != marginEnd(rows["b"][0]) || !strings.Contains(rows["b"][0], "\u2007\u2007−") {
		t.Fatalf("margins do not line up:\n%q\n%q", rows["a"][0], rows["b"][0])
	}

	// A ninth window has no menu slot, so it must not widen the column.
	windows := make([]provider.UsageWindow, maxWindowItems)
	for i := range windows {
		windows[i] = steady
	}
	rows = trayMenuRows(map[string]*provider.UsageData{"b": {Windows: append(windows, early)}}, menus)
	if len(rows["b"]) != maxWindowItems || strings.Contains(rows["b"][0], "\u2007\u2007−") {
		t.Fatalf("a hidden window widened the column: %q", rows["b"])
	}
}

func TestTrayDurationPadsTwoUnitsToAFixedWidth(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{2*time.Hour + 4*time.Minute, "\u20072h04m"},
		{12*time.Hour + 34*time.Minute, "12h34m"},
		{9 * time.Minute, "\u20070h09m"},
		{3*24*time.Hour + 18*time.Hour, "\u20073d18h"},
		{7 * 24 * time.Hour, "\u20077d\u20070h"},
		{-time.Minute, "\u20070h00m"},
	} {
		if got := trayDuration(tc.d); got != tc.want {
			t.Errorf("trayDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestTrayWindowRowsNameTheModelsOnlyWhenTwoWindowsShareALength(t *testing.T) {
	reset := time.Now().Add(48 * time.Hour)
	all := provider.UsageWindow{Name: "7d All", DisplayName: "7 days (all models)", Length: 7 * 24 * time.Hour, Utilization: 30, ResetsAt: reset}
	fable := provider.UsageWindow{Name: "7d Fable", DisplayName: "7 days (Fable)", Length: 7 * 24 * time.Hour, Utilization: 20, ResetsAt: reset}
	session := provider.UsageWindow{Name: "5h", DisplayName: "5 hours", Length: 5 * time.Hour, Utilization: 10, ResetsAt: time.Now().Add(time.Hour)}
	bonus := provider.UsageWindow{Name: "bonus", DisplayName: "Bonus", Utilization: 5, ResetsAt: reset}

	rows := testWindowRows(session, all)
	if strings.Contains(rows[1], "all models") || !strings.HasPrefix(rows[0], "5H   ") || !strings.HasPrefix(rows[1], "7D   ") {
		t.Fatalf("one seven-day window: %q", rows)
	}
	rows = testWindowRows(all, fable, bonus)
	if !strings.HasSuffix(rows[0], "  ·  all models") || !strings.HasSuffix(rows[1], "  ·  Fable") || !strings.HasPrefix(rows[2], "Bonus   ") || strings.Count(rows[2], "Bonus") != 1 {
		t.Fatalf("two seven-day windows: %q", rows)
	}
	if rows := testWindowRows(fable); !strings.HasSuffix(rows[0], "  ·  Fable") {
		t.Fatalf("a model-scoped window keeps its model: %q", rows)
	}
}
