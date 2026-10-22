// Package claudeapi shows prepaid Claude API credit pools saved by the
// Clawmeter browser extension. Anthropic has no API for the prepaid balance
// or its expiry, and its Admin API is unavailable to individual
// organizations, so the Console is the only source. This provider never
// reads browser credentials or calls the Console: it reads what the
// Clawmeter extension saved.
package claudeapi

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
	claudewebprovider "github.com/tnunamak/clawmeter/internal/provider/claudeweb"
)

const name = "claude_api"

// The one-click steps that clear a pool's warning. The tray row opens the
// Console for each.
const (
	signInAction  = "sign in to the Claude Console"
	updateAction  = "open your browser to update"
	consoleAction = "open the Claude Console"
)

// extensionContactWindow is how recently the browser extension must have
// reached Clawmeter for the browser to count as running.
const extensionContactWindow = 3 * time.Minute

// Provider reads saved pools. Claude sources are the local accounts a pool
// may be linked to through the Claude plan that funds it.
type Provider struct {
	claude []provider.Provider
}

func New(claude []provider.Provider) *Provider { return &Provider{claude: claude} }

func (*Provider) Name() string        { return name }
func (*Provider) DisplayName() string { return "Claude API" }
func (*Provider) Description() string {
	return "Prepaid Claude API credits, checked in the Claude Console"
}
func (*Provider) DashboardURL() string { return claudeweb.ConsoleURL }

// SourceRevision changes with every saved check, whenever a saved grant
// expires or the billing month ends, and when a linked Claude source signs in
// elsewhere, so a cached pool is never shown past the moment its numbers or
// its placement stop holding.
func (p *Provider) SourceRevision() string {
	pools, _ := claudeweb.ReadAPICreditPools()
	state, _ := claudeweb.ReadExtensionState()
	return p.sourceRevision(pools, time.Now(), state, claudeweb.LastExtensionContact())
}

func (p *Provider) sourceRevision(pools []claudeweb.APICreditPool, now time.Time, state claudeweb.ExtensionState, contact time.Time) string {
	parts := make([]string, 0, len(pools))
	for _, pool := range pools {
		expired := 0
		for _, grant := range pool.Grants {
			if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now) {
				expired++
			}
		}
		linked := p.linkedSource(pool)
		next := ""
		if linked != "" {
			if at := p.nextCreditAt(linked); !at.IsZero() {
				next = at.UTC().Format(time.RFC3339Nano)
			}
		}
		recent := now.Sub(contact) <= extensionContactWindow
		parts = append(parts, fmt.Sprintf("%s@%s:%d:%t:%s:%s:%t:%t:%s:%t", pool.Pool[:16], pool.ObservedAt.UTC().Format(time.RFC3339Nano), expired, now.Before(pool.MonthResetsAt), linked, pool.Source, poolLive(pool, now, state), poolSignedOut(pool, state), next, recent))
	}
	return "claude-api-credits-v1:" + strings.Join(parts, ",")
}

func (*Provider) IsConfigured() bool {
	pools, _ := claudeweb.ReadAPICreditPools()
	return len(pools) > 0
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.IsConfigured() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "API credits read by the browser extension"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "install the Clawmeter browser extension"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
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
		presented := Pool(pool, now)
		presented.Source = p.linkedSource(pool)
		presented.NextCreditAt = p.nextCreditAt(presented.Source)
		data.CreditPools = append(data.CreditPools, presented)
		if pool.ObservedAt.After(data.FetchedAt) {
			data.FetchedAt = pool.ObservedAt
		}
	}
	return data, nil
}

// linkedSource returns the key of the Claude source the pool was linked to,
// if that source is still signed in to the plan's organization.
func (p *Provider) linkedSource(pool claudeweb.APICreditPool) string {
	if pool.LinkedTarget == "" {
		return ""
	}
	for _, source := range p.claude {
		if provider.SourceKey(source) == pool.LinkedTarget && pool.LinkedTo(claudewebprovider.OrganizationUUID(source)) {
			return pool.LinkedTarget
		}
	}
	return ""
}

// nextCreditAt is when the linked Claude plan next grants its monthly credit,
// or zero when the source is unlinked or no reading of its current
// organization says so.
func (p *Provider) nextCreditAt(source string) time.Time {
	if source == "" {
		return time.Time{}
	}
	for _, claude := range p.claude {
		if provider.SourceKey(claude) == source {
			next, _ := claudeweb.NextCreditAt(source, claudewebprovider.OrganizationUUID(claude))
			return next
		}
	}
	return time.Time{}
}

// Pool turns a saved Console snapshot into a credit pool as of now.
func Pool(saved claudeweb.APICreditPool, now time.Time) provider.UsageCreditPool {
	state, _ := claudeweb.ReadExtensionState()
	return poolWithState(saved, now, state, claudeweb.LastExtensionContact())
}

func poolSignedOut(saved claudeweb.APICreditPool, state claudeweb.ExtensionState) bool {
	return saved.Source == "extension" && state.Status == "signed_out" && state.At.After(saved.ObservedAt)
}

func poolLive(saved claudeweb.APICreditPool, now time.Time, state claudeweb.ExtensionState) bool {
	return saved.Source == "extension" && now.Sub(saved.ObservedAt) <= 5*time.Minute && !poolSignedOut(saved, state)
}

// poolAction names the step that clears a pool's warning, or "" when it
// needs none. A stale extension reading with a recent contact means the
// browser runs but could not read this organization, so the fix is to sign in.
func poolAction(saved claudeweb.APICreditPool, pool provider.UsageCreditPool, now, contact time.Time) string {
	if saved.Source != "extension" {
		if !pool.ExpiredSinceCheck.IsZero() {
			return consoleAction
		}
		return ""
	}
	switch {
	case pool.SignedOut:
		return signInAction
	case pool.Live:
		return ""
	case now.Sub(contact) <= extensionContactWindow:
		return signInAction
	default:
		return updateAction
	}
}

func poolWithState(saved claudeweb.APICreditPool, now time.Time, state claudeweb.ExtensionState, contact time.Time) provider.UsageCreditPool {
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
	pool := provider.NewCreditPool(saved.Name, saved.Currency, saved.Balance, saved.MonthSpend, saved.MonthResetsAt, grants, daily, saved.ObservedAt, now)
	pool.Plan, pool.MonthlyCredit = saved.Plan, saved.MonthlyCredit
	pool.ObservationSource = saved.Source
	pool.SignedOut = poolSignedOut(saved, state)
	pool.Live = poolLive(saved, now, state)
	pool.Action = poolAction(saved, pool, now, contact)
	return pool
}
