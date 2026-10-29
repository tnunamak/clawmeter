package provider

import "time"

// OpenAIAPIBillingURL starts the provider's own link/claim flow. Opening it
// never activates a benefit or changes the receiving organization.
const OpenAIAPIBillingURL = "https://platform.openai.com/settings/organization/billing/overview"

// UsageCreditBenefit is an observed offer, not funded credit or usable quota.
// Monetary amounts are cents. Each offer has its own observation time, so a
// balance refresh cannot make an old eligibility decision current.
type UsageCreditBenefit struct {
	Name           string    `json:"name,omitempty"`
	Pool           string    `json:"pool,omitempty"`
	LinkObservedAt time.Time `json:"link_observed_at,omitzero"`
	Eligibility    string    `json:"eligibility"`
	LinkState      string    `json:"link_state"`
	Currency       string    `json:"currency,omitempty"`
	Claimable      *int64    `json:"claimable,omitempty"`
	PeriodCredit   *int64    `json:"period_credit,omitempty"`
	Cadence        string    `json:"cadence,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitzero"`
	ObservedAt     time.Time `json:"observed_at"`
}

// State evaluates the claim against the clock, not the provider's last poll.
func (b UsageCreditBenefit) State(now time.Time) string {
	if b.LinkState == "linked" {
		return "linked"
	}
	if b.ObservedAt.IsZero() || b.ObservedAt.After(now) || now.Sub(b.ObservedAt) >= 5*time.Minute {
		return "stale"
	}
	if b.Eligibility == "ineligible" {
		return "ineligible"
	}
	if b.Eligibility != "eligible" {
		return "unknown"
	}
	if !b.LinkObservedAt.IsZero() && (b.LinkObservedAt.After(now) || now.Sub(b.LinkObservedAt) >= 5*time.Minute) {
		return "unknown"
	}
	if b.LinkState != "unlinked" || b.Currency != "USD" || b.Claimable == nil || *b.Claimable <= 0 ||
		b.PeriodCredit == nil || *b.PeriodCredit <= 0 || *b.Claimable > *b.PeriodCredit ||
		*b.PeriodCredit > 100_000_000_000 || (b.Cadence != "monthly" && b.Cadence != "annual") || b.ExpiresAt.IsZero() {
		return "unknown"
	}
	if !b.ExpiresAt.After(now) {
		return "expired"
	}
	return "claimable"
}

// Summary is shared by the tray and CLI. It never presents an offer as a
// remaining balance, or keeps an expired/stale claim action alive.
func (b UsageCreditBenefit) Summary(now time.Time) string {
	if b.Name != "" {
		name := b.Name
		b.Name = ""
		text := b.Summary(now)
		if text != "" {
			return name + ": " + text
		}
		return ""
	}
	switch b.State(now) {
	case "claimable":
		period := "month"
		if b.Cadence == "annual" {
			period = "year"
		}
		return "Claim " + formatMinorUnits(int(*b.Claimable), b.Currency) + " in API credits (" +
			formatMinorUnits(int(*b.PeriodCredit), b.Currency) + "/" + period + ")…"
	case "linked":
		return "API credit benefit linked"
	case "ineligible":
		return ""
	default:
		return "Check API credit benefit…"
	}
}

// ActionURL opens an account-aware provider UI for the user to review. The
// collector does not accept action URLs from page messages or cached input.
func (b UsageCreditBenefit) ActionURL(now time.Time) string {
	if b.State(now) == "ineligible" || b.State(now) == "linked" {
		return ""
	}
	return OpenAIAPIBillingURL
}

func (b UsageCreditBenefit) Clone() UsageCreditBenefit {
	if b.Claimable != nil {
		n := *b.Claimable
		b.Claimable = &n
	}
	if b.PeriodCredit != nil {
		n := *b.PeriodCredit
		b.PeriodCredit = &n
	}
	return b
}
