// Package claudeweb exposes browser-confirmed Claude reset credits as a hidden
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
func (*Provider) DashboardURL() string   { return "https://claude.ai/settings/usage" }
func (p *Provider) SourceID() string     { return p.sourceID }
func (p *Provider) SourceLabel() string  { return p.label }
func (p *Provider) SourceRevision() string {
	return "claude-web-snapshot-v3:" + p.targetKey + ":" + p.targetSourceRevision()
}
func (p *Provider) IsConfigured() bool {
	summary, err := p.readSummary()
	return err == nil && p.matchesSourceRevision(summary) && availableCount(summary, time.Now()) > 0
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.IsConfigured() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "browser reset observation available"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "run a browser check from the tray"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	summary, err := p.readSummary()
	if err != nil {
		return nil, fmt.Errorf("read Claude Web reset inventory: %w", err)
	}
	if !p.matchesSourceRevision(summary) {
		return nil, fmt.Errorf("saved Claude reset observation belongs to a different local source revision")
	}
	credits := make([]provider.UsageResetCredit, 0, len(summary.Credits))
	for _, expiresAt := range summary.Credits {
		credits = append(credits, provider.UsageResetCredit{Status: "available", ExpiresAt: expiresAt})
	}
	available := summary.AvailableCount
	if len(summary.Credits) > 0 {
		available = availableCount(summary, time.Now())
	}
	return &provider.UsageData{
		Provider:           name,
		SourceID:           p.sourceID,
		SourceLabel:        p.label,
		ResetCreditsTarget: p.targetKey,
		FetchedAt:          summary.ObservedAt,
		ResetCredits: &provider.UsageResetCredits{
			AvailableCount:    available,
			EarliestExpiresAt: summary.EarliestExpiresAt,
			Credits:           credits,
			FetchedAt:         summary.ObservedAt,
			Snapshot:          true,
		},
	}, nil
}

// StartCheck starts a one-time browser handoff for the selected Claude source.
// The tray calls this; keeping the source-revision choice here guarantees the
// session stamps the same identity that FetchUsage later validates against.
func StartCheck(target provider.Provider) (*claudeweb.Session, error) {
	return claudeweb.StartForSource(provider.SourceKey(target), ResetSnapshotRevision(target))
}

// ResetSnapshotRevision is the identity a browser reset snapshot is bound to.
// It is not the cache SourceRevision: that one rotates with OAuth token
// refreshes and differs for the pinned native Default source.
func ResetSnapshotRevision(target provider.Provider) string {
	source, ok := target.(interface{ ResetSnapshotSourceRevision() string })
	if !ok {
		return ""
	}
	return source.ResetSnapshotSourceRevision()
}

func (p *Provider) readSummary() (claudeweb.GrantSummary, error) {
	return claudeweb.ReadSummaryFor(p.targetKey)
}

func (p *Provider) targetSourceRevision() string {
	return ResetSnapshotRevision(p.target)
}

func (p *Provider) matchesSourceRevision(summary claudeweb.GrantSummary) bool {
	return summary.SourceRevision == p.targetSourceRevision()
}

func availableCount(summary claudeweb.GrantSummary, now time.Time) int {
	if len(summary.Credits) == 0 {
		if summary.AvailableCount > 0 && (summary.EarliestExpiresAt.IsZero() || summary.EarliestExpiresAt.After(now)) {
			return summary.AvailableCount
		}
		return 0
	}
	count := 0
	for _, expiresAt := range summary.Credits {
		if expiresAt.After(now) {
			count++
		}
	}
	return count
}
