package view

import (
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// CreditBenefit is an offer evaluated at the snapshot time. A missing
// Claimable value is not a zero-dollar grant or an active claim invitation.
type CreditBenefit struct {
	provider.UsageCreditBenefit
	State     string `json:"state"`
	Text      string `json:"text"`
	ActionURL string `json:"action_url,omitempty"`
}

// APIBalance separates the historical receipt from currently usable money.
type APIBalance struct {
	provider.UsageAPICreditBalance
	State     string `json:"state"`
	Available *int64 `json:"available,omitempty"`
	Text      string `json:"text"`
	ActionURL string `json:"action_url"`
}

func buildAPIBalance(raw provider.UsageAPICreditBalance, at time.Time) APIBalance {
	raw.Grants = append([]provider.APICreditExpiry(nil), raw.Grants...)
	b := APIBalance{UsageAPICreditBalance: raw, State: raw.State(at), Text: raw.Summary(at), ActionURL: provider.OpenAIAPIBillingURL}
	if b.State == "current" {
		n := raw.Balance
		b.Available = &n
	}
	return b
}

func buildCreditBenefit(raw provider.UsageCreditBenefit, at time.Time) CreditBenefit {
	b := CreditBenefit{UsageCreditBenefit: raw.Clone(), State: raw.State(at), Text: raw.Summary(at), ActionURL: raw.ActionURL(at)}
	if b.State != "claimable" {
		b.Claimable = nil
	}
	return b
}
