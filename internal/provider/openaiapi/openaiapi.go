// Package openaiapi reads normalized browser receipts. It never reads browser
// credentials, calls inference, or activates the advertised subscription benefit.
package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"github.com/tnunamak/clawmeter/internal/provider"
)

type Provider struct{ Store *openaicredits.Store }

func New() *Provider                  { return &Provider{} }
func (*Provider) Name() string        { return "openai_api" }
func (*Provider) DisplayName() string { return "OpenAI API" }
func (*Provider) Description() string {
	return "API credit balances and subscription offers read from OpenAI billing"
}
func (*Provider) DashboardURL() string { return provider.OpenAIAPIBillingURL }
func (p *Provider) read() ([]openaicredits.Snapshot, error) {
	store := p.Store
	if store == nil {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		store = &openaicredits.Store{Dir: filepath.Join(base, "clawmeter", "openai-api-credits")}
	}
	return store.ReadAll()
}
func (p *Provider) IsConfigured() bool { saved, _ := p.read(); return len(saved) > 0 }
func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.IsConfigured() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "Saved OpenAI billing readings"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "Enable OpenAI billing in the Clawmeter browser extension, then open billing"}
}
func (p *Provider) SourceRevision() string {
	saved, _ := p.read()
	data, _ := json.Marshal(saved)
	sum := sha256.Sum256(data)
	return "openai-api:" + hex.EncodeToString(sum[:])
}
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	saved, err := p.read()
	if err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, fmt.Errorf("open OpenAI billing with the Clawmeter extension enabled")
	}
	data := &provider.UsageData{Provider: p.Name()}
	for _, receipt := range saved {
		if c := receipt.Credits; c != nil {
			balance := provider.UsageAPICreditBalance{Pool: receipt.Pool, Name: receipt.Name, Currency: c.Currency, Balance: c.Balance, ObservedAt: c.ObservedAt}
			for _, grant := range c.Grants {
				balance.Grants = append(balance.Grants, provider.APICreditExpiry{Remaining: grant.Remaining, ExpiresAt: grant.ExpiresAt})
			}
			data.APICreditBalances = append(data.APICreditBalances, balance)
			if c.ObservedAt.After(data.FetchedAt) {
				data.FetchedAt = c.ObservedAt
			}
		}
		if b := receipt.Benefit; b != nil {
			benefit := provider.UsageCreditBenefit{Name: receipt.Name, Pool: receipt.Pool, Eligibility: "ineligible", LinkState: "unknown", Currency: b.Currency, Claimable: b.ClaimableCredit, Cadence: b.Cadence, ExpiresAt: b.ExpiresAt, ObservedAt: b.ObservedAt}
			if *b.Eligible {
				benefit.Eligibility = "eligible"
				amount := b.PeriodCredit
				benefit.PeriodCredit = &amount
			}
			if receipt.Link != nil {
				benefit.LinkState = receipt.Link.State
				benefit.LinkObservedAt = receipt.Link.ObservedAt
			}
			data.CreditBenefits = append(data.CreditBenefits, benefit.Clone())
			if b.ObservedAt.After(data.FetchedAt) {
				data.FetchedAt = b.ObservedAt
			}
		}
	}
	return data, nil
}
