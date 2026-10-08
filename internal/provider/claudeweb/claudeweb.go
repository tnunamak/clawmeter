// Package claudeweb exposes browser-observed Claude reset credits as a hidden
// supplemental source. Each instance is bound to one Claude source key and is
// merged under that Claude account for display (provider.PresentResetCreditSources);
// it is never a user-facing provider. It never reads browser credentials or
// polls Claude.
package claudeweb

import (
	"context"
	"fmt"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const name = "claude_web"

type Provider struct {
	targetKey string
	sourceID  string
	label     string
	target    provider.Provider
}

func NewFor(source provider.Provider) *Provider {
	sourceID := provider.SourceID(source)
	label := provider.SourceLabel(source)
	if label == "" {
		label = sourceID
	}
	return &Provider{targetKey: provider.SourceKey(source), sourceID: sourceID, label: label, target: source}
}

func (p *Provider) Name() string         { return name }
func (*Provider) ProviderFamily() string { return "claude" }
func (*Provider) DisplayName() string    { return "Claude" }
func (p *Provider) Description() string  { return "Browser-observed Claude reset inventory" }
func (*Provider) DashboardURL() string   { return claudeweb.UsageURL }
func (p *Provider) SourceID() string     { return p.sourceID }
func (p *Provider) SourceLabel() string  { return p.label }

// SourceRevision changes with every new check (fresh salt) and when the
// source's account no longer matches the snapshot, so cached copies of an
// invalid snapshot are never shown.
func (p *Provider) SourceRevision() string {
	summary, ok := p.snapshot()
	if !ok {
		return "claude-web-snapshot-v4:" + p.targetKey + ":none"
	}
	return "claude-web-snapshot-v4:" + p.targetKey + ":" + summary.MatchHash
}

func (p *Provider) IsConfigured() bool {
	summary, ok := p.snapshot()
	return ok && availableCount(summary, time.Now()) > 0
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.IsConfigured() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "browser reset check available"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "choose Check Claude resets in the tray"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	summary, ok := p.snapshot()
	if !ok {
		return nil, fmt.Errorf("no Claude reset check for this account")
	}
	now := time.Now()
	credits := make([]provider.UsageResetCredit, 0, len(summary.Credits))
	earliest := time.Time{}
	for _, expiresAt := range summary.Credits {
		credits = append(credits, provider.UsageResetCredit{Status: "available", ExpiresAt: expiresAt})
		if expiresAt.After(now) && (earliest.IsZero() || expiresAt.Before(earliest)) {
			earliest = expiresAt
		}
	}
	return &provider.UsageData{
		Provider:           name,
		SourceID:           p.sourceID,
		SourceLabel:        p.label,
		ResetCreditsTarget: p.targetKey,
		FetchedAt:          summary.ObservedAt,
		ResetCredits: &provider.UsageResetCredits{
			AvailableCount:    availableCount(summary, now),
			EarliestExpiresAt: earliest,
			Credits:           credits,
			FetchedAt:         summary.ObservedAt,
			Snapshot:          true,
		},
	}, nil
}

// snapshot returns the saved check only while the source is still signed in
// to the organization the browser proved.
func (p *Provider) snapshot() (claudeweb.GrantSummary, bool) {
	summary, err := claudeweb.ReadSummaryFor(p.targetKey)
	if err != nil || !summary.MatchesOrg(OrganizationUUID(p.target)) {
		return claudeweb.GrantSummary{}, false
	}
	return summary, true
}

// OrganizationUUID returns the Claude organization a local Claude source is
// signed in to, or "" when unknown.
func OrganizationUUID(source provider.Provider) string {
	if s, ok := source.(interface{ ClaudeOrganizationUUID() string }); ok {
		return s.ClaudeOrganizationUUID()
	}
	return ""
}

// Accounts lists the Claude sources a browser check can be filed under,
// labelled as the tray names them.
func Accounts(sources []provider.Provider) []claudeweb.Account {
	accounts := make([]claudeweb.Account, 0, len(sources))
	for _, source := range sources {
		if source.Name() == "claude" {
			accounts = append(accounts, claudeweb.Account{Key: provider.SourceKey(source), Label: provider.SourceLabel(source), OrgUUID: OrganizationUUID(source)})
		}
	}
	for i := range accounts {
		if accounts[i].Label == "" && len(accounts) > 1 {
			accounts[i].Label = "Default"
		} else if accounts[i].Label == "" {
			accounts[i].Label = "Claude"
		}
	}
	return accounts
}

func availableCount(summary claudeweb.GrantSummary, now time.Time) int {
	count := 0
	for _, expiresAt := range summary.Credits {
		if expiresAt.After(now) {
			count++
		}
	}
	return count
}
