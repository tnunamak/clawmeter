// Package openaiapi shows OpenAI API credit: the ChatGPT subscription benefit
// and the funded balance of each API organization. The benefit is read through
// each Codex source's ChatGPT login on every poll. The funded balance is
// readable only with a Platform browser session, so it comes from receipts the
// browser extension saved. Nothing here calls inference or links, claims or
// buys credit.
package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// BenefitReader is a Codex source that can read the API credit benefit.
type BenefitReader interface {
	HasChatGPTAuth() bool
	ReadAPICreditBenefits(context.Context) ([]provider.UsageCreditBenefit, error)
}

type Provider struct {
	Store *openaicredits.Store
	codex []provider.Provider
	// maxAge is how long a polled benefit reading stays current: the
	// reading age every other polled source uses.
	maxAge time.Duration
}

// New reads the benefit through the given Codex sources. A zero maxAge
// leaves polled readings on the five-minute snapshot rule.
func New(codex []provider.Provider, maxAge time.Duration) *Provider {
	return &Provider{codex: codex, maxAge: maxAge}
}
func (*Provider) Name() string        { return "openai_api" }
func (*Provider) DisplayName() string { return "OpenAI API" }
func (*Provider) Description() string {
	return "API credits included with ChatGPT Pro and Business, and API credit balances"
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

func (p *Provider) readers() []BenefitReader {
	var readers []BenefitReader
	for _, source := range p.codex {
		if r, ok := source.(BenefitReader); ok && r.HasChatGPTAuth() {
			readers = append(readers, r)
		}
	}
	return readers
}

func (p *Provider) IsConfigured() bool {
	if len(p.readers()) > 0 {
		return true
	}
	saved, _ := p.read()
	return len(saved) > 0
}
func (p *Provider) SetupStatus() provider.SetupStatus {
	if len(p.readers()) > 0 {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "API credit benefit read through your Codex login"}
	}
	if p.IsConfigured() {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "Saved OpenAI billing readings"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "Sign in to Codex with ChatGPT, or enable OpenAI billing in the Clawmeter browser extension"}
}

// SourceRevision changes with the saved receipts and with the Codex logins
// the benefit is read through, so a cached benefit never outlives its login.
func (p *Provider) SourceRevision() string {
	saved, _ := p.read()
	logins := make([]string, 0, len(p.codex))
	for _, source := range p.codex {
		logins = append(logins, provider.SourceKey(source)+"="+provider.SourceRevision(source))
	}
	data, _ := json.Marshal(struct {
		Saved  []openaicredits.Snapshot
		Logins []string
	}{saved, logins})
	sum := sha256.Sum256(data)
	return "openai-api:" + hex.EncodeToString(sum[:])
}

// readBenefits reads every Codex login. Two logins can see one organization:
// eligibility belongs to each ChatGPT account, so an eligible offer is shown
// over another account's refusal; link state belongs to the organization, so
// the newest link reading wins and a fresh "linked" retracts any claim.
func (p *Provider) readBenefits(ctx context.Context) ([]provider.UsageCreditBenefit, error) {
	var result []provider.UsageCreditBenefit
	var firstErr error
	index := map[string]int{}
	for _, reader := range p.readers() {
		benefits, err := reader.ReadAPICreditBenefits(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, b := range benefits {
			i, seen := index[b.Pool]
			if !seen {
				index[b.Pool] = len(result)
				result = append(result, b)
				continue
			}
			result[i] = mergeReadings(result[i], b)
		}
	}
	for i := range result {
		if p.maxAge > 0 {
			observed := result[i].ObservedAt
			if result[i].LinkObservedAt.Before(observed) {
				observed = result[i].LinkObservedAt
			}
			result[i].FreshUntil = observed.Add(p.maxAge)
		}
	}
	return result, firstErr
}

func mergeReadings(kept, other provider.UsageCreditBenefit) provider.UsageCreditBenefit {
	merged := kept
	if other.Eligibility == "eligible" && (kept.Eligibility != "eligible" || other.ObservedAt.After(kept.ObservedAt)) {
		merged = other
	}
	link := kept
	if other.LinkObservedAt.After(kept.LinkObservedAt) {
		link = other
	}
	merged.LinkState, merged.LinkObservedAt = link.LinkState, link.LinkObservedAt
	return merged.Clone()
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	saved, err := p.read()
	if err != nil {
		return nil, err
	}
	direct, directErr := p.readBenefits(ctx)
	if len(saved) == 0 && len(direct) == 0 {
		if directErr != nil {
			return nil, directErr
		}
		if len(p.readers()) == 0 {
			return nil, fmt.Errorf("open OpenAI billing with the Clawmeter extension enabled")
		}
	}
	data := &provider.UsageData{Provider: p.Name()}
	polled := map[string]bool{}
	for _, benefit := range direct {
		polled[benefit.Pool] = true
		data.CreditBenefits = append(data.CreditBenefits, benefit.Clone())
		if benefit.ObservedAt.After(data.FetchedAt) {
			data.FetchedAt = benefit.ObservedAt
		}
	}
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
		// This poll's reading of the organization supersedes a browser
		// snapshot of the same offer.
		if b := receipt.Benefit; b != nil && !polled[receipt.Pool] {
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
