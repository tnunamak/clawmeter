// Package claudeapi shows prepaid Claude API credit pools saved by a browser
// check of the Claude Console. Anthropic has no API for the prepaid balance
// or its expiry, and its Admin API is unavailable to individual
// organizations, so the Console is the only source. This provider never
// reads browser credentials or calls the Console: it reads what the
// Clawmeter bookmark saved.
package claudeapi

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const name = "claude_api"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (*Provider) Name() string        { return name }
func (*Provider) DisplayName() string { return "Claude API" }
func (*Provider) Description() string {
	return "Prepaid Claude API credits, checked in the Claude Console"
}
func (*Provider) DashboardURL() string { return claudeweb.ConsoleURL }

// SourceRevision changes with every saved check, and whenever a saved grant
// expires or the billing month ends, so a cached pool is never shown past
// the moment its numbers stop holding.
func (*Provider) SourceRevision() string {
	pools, _ := claudeweb.ReadAPICreditPools()
	now := time.Now()
	parts := make([]string, 0, len(pools))
	for _, pool := range pools {
		expired := 0
		for _, grant := range pool.Grants {
			if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now) {
				expired++
			}
		}
		parts = append(parts, fmt.Sprintf("%s@%s:%d:%t", pool.Pool[:16], pool.ObservedAt.UTC().Format(time.RFC3339Nano), expired, now.Before(pool.MonthResetsAt)))
	}
	return "claude-api-credits-v1:" + strings.Join(parts, ",")
}

func (*Provider) IsConfigured() bool {
	pools, _ := claudeweb.ReadAPICreditPools()
	return len(pools) > 0
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.IsConfigured() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "API credits checked in the Claude Console"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "choose Check API credits in the tray"}
}

func (*Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	saved, err := claudeweb.ReadAPICreditPools()
	if err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, fmt.Errorf("no API credit check yet")
	}
	now := time.Now()
	data := &provider.UsageData{Provider: name}
	for _, pool := range saved {
		data.CreditPools = append(data.CreditPools, Pool(pool, now))
		if pool.ObservedAt.After(data.FetchedAt) {
			data.FetchedAt = pool.ObservedAt
		}
	}
	return data, nil
}

// Pool turns a saved Console snapshot into a credit pool as of now.
func Pool(saved claudeweb.APICreditPool, now time.Time) provider.UsageCreditPool {
	grants := make([]provider.CreditGrant, 0, len(saved.Grants))
	for _, grant := range saved.Grants {
		grants = append(grants, provider.CreditGrant{Remaining: grant.Remaining, ExpiresAt: grant.ExpiresAt})
	}
	daily := make([]provider.DailySpend, 0, len(saved.DailySpend))
	for day, amount := range saved.DailySpend {
		if parsed, err := time.Parse(time.DateOnly, day); err == nil {
			daily = append(daily, provider.DailySpend{Day: parsed, Amount: amount})
		}
	}
	sort.Slice(daily, func(i, j int) bool { return daily[i].Day.Before(daily[j].Day) })
	return provider.NewCreditPool(saved.Name, saved.Currency, saved.Balance, saved.MonthSpend, saved.MonthResetsAt, grants, daily, saved.ObservedAt, now)
}
