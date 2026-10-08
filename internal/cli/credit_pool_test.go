package cli

import (
	"strings"
	"testing"
	"time"

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
