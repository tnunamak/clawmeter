package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func creditPoolOutput(pool provider.UsageCreditPool) *MultiProviderOutput {
	return &MultiProviderOutput{Providers: []ProviderFormatter{{
		Name: "claude_api", Display: "Claude API", Data: &provider.UsageData{Provider: "claude_api", CreditPools: []provider.UsageCreditPool{pool}},
	}}}
}

func TestAgentSummaryListsAPICreditPools(t *testing.T) {
	observed := time.Now().Add(-time.Hour)
	expires := observed.Add(16 * 24 * time.Hour)
	pool := provider.NewCreditPool("Family's Individual Organization", "USD", 19988, 11, observed.Add(20*24*time.Hour),
		[]provider.CreditGrant{{Remaining: 19988, ExpiresAt: expires}},
		[]provider.DailySpend{{Day: observed.UTC().Truncate(24 * time.Hour), Amount: 11.2965}}, observed, time.Now())
	got := creditPoolOutput(pool).AgentSummary()
	for _, want := range []string{
		"Quota: no active quota data; api_credits=[Claude API · Family's Individual Organization currency=USD balance=199.88 spent_this_month=0.11",
		"expiring_amount=199.88", "projected_spend_before_expiry=", "projected_unused_at_expiry=", "snapshot=true]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AgentSummary() = %q, missing %q", got, want)
		}
	}
	if line := creditPoolOutput(pool).Providers[0].FormatPlain(); !strings.HasPrefix(line, "Claude API: Family's Individual Organization: $199.88 left · spent $0.11 this month · on pace to spend $") {
		t.Errorf("FormatPlain() = %q", line)
	}
}

func TestAgentSummaryNeverOffersExpiredCreditsAsBudget(t *testing.T) {
	observed := time.Now().Add(-11 * 24 * time.Hour)
	pool := provider.NewCreditPool("Org", "USD", 20000, 0, time.Now().Add(24*time.Hour),
		[]provider.CreditGrant{{Remaining: 20000, ExpiresAt: time.Now().Add(-24 * time.Hour)}}, nil, observed, time.Now())
	got := creditPoolOutput(pool).AgentSummary()
	if !strings.Contains(got, "Claude API · Org status=expired_since_check balance=unknown") || strings.Contains(got, "balance=200.00") {
		t.Fatalf("AgentSummary() = %q", got)
	}
}

type claudeAPICLIProvider struct{}

func (claudeAPICLIProvider) Name() string         { return "claude_api" }
func (claudeAPICLIProvider) DisplayName() string  { return "Claude API" }
func (claudeAPICLIProvider) Description() string  { return "" }
func (claudeAPICLIProvider) DashboardURL() string { return "" }
func (claudeAPICLIProvider) IsConfigured() bool   { return true }
func (claudeAPICLIProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return nil, nil
}

// When every pool folds under its Claude source, an explicitly enabled Claude
// API provider must not leave an empty row behind, fresh or cached.
func TestFoldedCreditPoolsLeaveNoEmptyRow(t *testing.T) {
	registry := provider.NewRegistry()
	for _, p := range []provider.Provider{sourcedCLIProvider{id: "default"}, claudeAPICLIProvider{}} {
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.Providers["claude_api"] = config.ProviderConfig{Enabled: true}
	results := map[string]*provider.UsageData{
		"claude":     {Provider: "claude", SourceID: "default", Windows: []provider.UsageWindow{{Name: "weekly", Utilization: 10, ResetsAt: time.Now().Add(time.Hour)}}},
		"claude_api": {Provider: "claude_api", CreditPools: []provider.UsageCreditPool{{Name: "Org", Organization: "Org", Source: "claude", Currency: "USD", Balance: 100, ObservedAt: time.Now()}}},
	}
	entry := &cache.Entry{ProviderData: results, SourceRevisions: map[string]string{}}
	for name, output := range map[string]*MultiProviderOutput{
		"fresh":  buildOutputFromResult(registry, cfg, &provider.MultiFetchResult{Results: results}, nil),
		"cached": buildOutputFromCache(registry, cfg, entry),
	} {
		output.HideUnavailable()
		if len(output.Providers) != 1 || output.Providers[0].Name != "claude" || len(output.Providers[0].Data.CreditPools) != 1 {
			t.Errorf("%s: rows = %+v", name, output.Providers)
		}
	}
}

func TestAgentCreditPoolFreshness(t *testing.T) {
	now := time.Now()
	pool := provider.UsageCreditPool{Name: "Org", Currency: "USD", Balance: 100, ObservedAt: now, Live: true, ObservationSource: "extension"}
	got := creditPoolOutput(pool).AgentSummary()
	if !strings.Contains(got, "live=true") || strings.Contains(got, "updated_at=") || !strings.Contains(got, "snapshot=false") {
		t.Fatalf("live summary = %q", got)
	}
	pool.ObservedAt = now.Add(-6 * time.Minute)
	got = creditPoolOutput(pool).AgentSummary()
	if !strings.Contains(got, "live=false") || !strings.Contains(got, "updated_at="+pool.ObservedAt.Local().Format(time.RFC3339)) {
		t.Fatalf("stale summary = %q", got)
	}
	pool.ExpiredSinceCheck = now.Add(-time.Minute)
	got = creditPoolOutput(pool).AgentSummary()
	if !strings.Contains(got, "live=false") || !strings.Contains(got, "updated_at=") || !strings.Contains(got, "balance=unknown") {
		t.Fatalf("expired summary = %q", got)
	}
}
