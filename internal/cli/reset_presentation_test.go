package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// claudeUsageStates are the Claude usage conditions under which a browser
// reset observation must still be shown under the Claude account.
func claudeUsageStates(now time.Time) map[string]*provider.UsageData {
	healthy := []provider.UsageWindow{{Name: "7d All", Utilization: 40, ResetsAt: now.Add(48 * time.Hour)}}
	return map[string]*provider.UsageData{
		"healthy":      {Provider: "claude", SourceID: "default", FetchedAt: now, Windows: healthy},
		"stale":        {Provider: "claude", SourceID: "default", FetchedAt: now.Add(-time.Hour), Stale: true, Warning: "rate limited", Windows: healthy},
		"errored":      {Provider: "claude", SourceID: "default", Error: "usage request failed (500)"},
		"rate limited": {Provider: "claude", SourceID: "default", Error: "HTTP 429 rate limit exceeded"},
		"expired":      {Provider: "claude", SourceID: "default", IsExpired: true, Error: "token expired"},
		"unavailable":  nil,
	}
}

func browserResetSnapshot(now time.Time) *provider.UsageData {
	return &provider.UsageData{
		Provider: "claude_web", SourceID: "default", SourceLabel: "Default", ResetCreditsTarget: "claude", FetchedAt: now,
		ResetCredits: &provider.UsageResetCredits{
			Snapshot: true, AvailableCount: 1, FetchedAt: now,
			EarliestExpiresAt: now.Add(15 * 24 * time.Hour),
			Credits:           []provider.UsageResetCredit{{Status: "available", ExpiresAt: now.Add(15 * 24 * time.Hour)}},
		},
	}
}

func assertResetUnderClaude(t *testing.T, output *MultiProviderOutput, state string) {
	t.Helper()
	if len(output.Providers) != 1 || output.Providers[0].Name != "claude" || output.Providers[0].Display != "Claude" {
		t.Fatalf("%s: rows = %#v, want exactly one Claude row", state, output.Providers)
	}
	row := output.Providers[0]
	if row.Data == nil || row.Data.ResetCredits == nil || row.Data.ResetCredits.DisplayCount(time.Now()) != 1 {
		t.Fatalf("%s: Claude row lacks the reset snapshot: %#v", state, row.Data)
	}
	plain := row.FormatPlain()
	for _, want := range []string{"Claude:", "reset credits: 1 observed", "last observed "} {
		if !strings.Contains(plain, want) {
			t.Errorf("%s: plain output %q missing %q", state, plain, want)
		}
	}
	if strings.Contains(plain, "claude_web") || strings.Contains(plain, "Web") {
		t.Errorf("%s: plain output leaks the supplemental source: %q", state, plain)
	}

	agent := output.AgentSummary()
	if !strings.Contains(agent, "reset_credits=[Claude observed_count=1 snapshot=true account_match=user_confirmed") ||
		!strings.Contains(agent, "last_observed_at=") {
		t.Errorf("%s: agent summary %q missing the Claude reset observation", state, agent)
	}

	jsonOut := captureStdout(t, func() { output.PrintJSON(nil) })
	var decoded struct {
		Providers map[string]struct {
			Usage struct {
				ResetCredits *struct {
					AvailableCount int  `json:"available_count"`
					Snapshot       bool `json:"snapshot"`
				} `json:"reset_credits"`
			} `json:"usage"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatalf("%s: invalid JSON %q: %v", state, jsonOut, err)
	}
	if len(decoded.Providers) != 1 {
		t.Errorf("%s: JSON providers = %v, want only claude", state, decoded.Providers)
	}
	claude, ok := decoded.Providers["claude"]
	if !ok || claude.Usage.ResetCredits == nil || claude.Usage.ResetCredits.AvailableCount != 1 || !claude.Usage.ResetCredits.Snapshot {
		t.Errorf("%s: JSON claude usage lacks reset credits: %s", state, jsonOut)
	}
	if strings.Contains(jsonOut, "claude_web") {
		t.Errorf("%s: JSON leaks the supplemental source: %s", state, jsonOut)
	}
}

func TestResetSnapshotShowsUnderClaudeInEveryOutputFromFreshResults(t *testing.T) {
	now := time.Now()
	for state, usage := range claudeUsageStates(now) {
		t.Run(state, func(t *testing.T) {
			registry := provider.NewRegistry()
			if err := registry.Register(sourcedCLIProvider{id: "default"}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Register(sourcedClaudeWebCLIProvider{id: "default"}); err != nil {
				t.Fatal(err)
			}
			results := map[string]*provider.UsageData{"claude_web": browserResetSnapshot(now)}
			if usage != nil {
				results["claude"] = usage
			}
			output := buildOutputFromResult(registry, config.DefaultConfig(), &provider.MultiFetchResult{Results: results}, nil)
			output.HideUnavailable()
			assertResetUnderClaude(t, output, state)
		})
	}
}

func TestResetSnapshotShowsUnderClaudeInEveryOutputFromCache(t *testing.T) {
	now := time.Now()
	for state, usage := range claudeUsageStates(now) {
		t.Run(state, func(t *testing.T) {
			registry := provider.NewRegistry()
			if err := registry.Register(sourcedCLIProvider{id: "default"}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Register(sourcedClaudeWebCLIProvider{id: "default"}); err != nil {
				t.Fatal(err)
			}
			entry := &cache.Entry{
				ProviderData:    map[string]*provider.UsageData{"claude_web": browserResetSnapshot(now)},
				SourceRevisions: map[string]string{"claude_web": provider.SourceRevision(sourcedClaudeWebCLIProvider{id: "default"})},
			}
			if usage != nil {
				entry.ProviderData["claude"] = usage
			}
			output := buildOutputFromCache(registry, config.DefaultConfig(), entry)
			output.HideUnavailable()
			assertResetUnderClaude(t, output, state)
			// The status line reads the same cache path; it must not crash or
			// mention the supplemental source.
			if line := output.StatusLineSummary(); strings.Contains(line, "claude_web") {
				t.Errorf("%s: status line leaks supplemental source: %q", state, line)
			}
		})
	}
}

func TestResetSnapshotStaysSeparatePerClaudeSourceInOutput(t *testing.T) {
	now := time.Now()
	registry := provider.NewRegistry()
	for _, p := range []provider.Provider{sourcedCLIProvider{id: "default"}, sourcedCLIProvider{id: "odl"}, sourcedClaudeWebCLIProvider{id: "odl"}} {
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := browserResetSnapshot(now)
	snapshot.SourceID, snapshot.SourceLabel, snapshot.ResetCreditsTarget = "odl", "ODL", "claude:odl"
	results := map[string]*provider.UsageData{
		"claude":         {Provider: "claude", SourceID: "default", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 10, ResetsAt: now.Add(time.Hour)}}},
		"claude:odl":     {Provider: "claude", SourceID: "odl", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 20, ResetsAt: now.Add(time.Hour)}}},
		"claude_web:odl": snapshot,
	}
	output := buildOutputFromResult(registry, config.DefaultConfig(), &provider.MultiFetchResult{Results: results}, nil)
	output.HideUnavailable()
	if len(output.Providers) != 2 {
		t.Fatalf("rows = %#v, want the two Claude sources", output.Providers)
	}
	for _, row := range output.Providers {
		hasReset := row.Data != nil && row.Data.ResetCredits != nil
		if (row.Name == "claude:odl") != hasReset {
			t.Fatalf("source %q reset presence = %v; the ODL snapshot must attach only to ODL", row.Name, hasReset)
		}
	}
}

func TestAgentSummaryKeepsResetObservationWhenNoQuotaIsReadable(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{
		Name: "claude", Display: "Claude", Data: &provider.UsageData{Provider: "claude", SourceID: "default", Error: "usage request failed (500)", ResetCredits: browserResetSnapshot(now).ResetCredits},
	}}}
	got := output.AgentSummary()
	if !strings.HasPrefix(got, "Quota: no active quota data; reset_credits=[Claude observed_count=1") {
		t.Fatalf("AgentSummary() = %q", got)
	}
	if strings.Contains((&MultiProviderOutput{}).AgentSummary(), "reset_credits") {
		t.Fatal("empty output must not mention reset credits")
	}
}

func TestSupplementalResetSourcesFollowTheirClaudeSources(t *testing.T) {
	registry := provider.NewRegistry()
	claudeDefault, claudeODL := sourcedCLIProvider{id: "default"}, sourcedCLIProvider{id: "odl"}
	for _, p := range []provider.Provider{claudeDefault, claudeODL, sourcedClaudeWebCLIProvider{id: "odl"}, unconfiguredSourcedClaudeWebCLIProvider{sourcedClaudeWebCLIProvider{id: "default"}}} {
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	got := supplementalResetSources(registry, "claude", []provider.Provider{claudeDefault, claudeODL})
	if len(got) != 1 || provider.SourceKey(got[0]) != "claude_web:odl" {
		t.Fatalf("supplemental sources = %v, want only the configured ODL observation", got)
	}
	if other := supplementalResetSources(registry, "codex", []provider.Provider{claudeODL}); len(other) != 0 {
		t.Fatalf("non-Claude family got supplemental sources: %v", other)
	}
}
