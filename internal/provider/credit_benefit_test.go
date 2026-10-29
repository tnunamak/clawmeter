package provider

import (
	"strings"
	"testing"
	"time"
)

func TestCreditBenefitIsAnOfferNotABalance(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	claim, period := int64(3500), int64(7000)
	b := UsageCreditBenefit{Eligibility: "eligible", LinkState: "unlinked", Currency: "USD",
		Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	if got := b.Summary(now); got != "Claim $35.00 in API credits ($70.00/month)…" {
		t.Fatal(got)
	}
	if b.ActionURL(now) != OpenAIAPIBillingURL {
		t.Fatal("claim must open the provider's review flow")
	}
	for _, at := range []time.Time{now.Add(-time.Second), now.Add(5*time.Minute + time.Second), b.ExpiresAt} {
		if strings.Contains(b.Summary(at), "Claim $") {
			t.Fatal("old/future/expired observations advertised a claim")
		}
	}
	b.Cadence = "annual"
	if !strings.Contains(b.Summary(now), "$70.00/year") {
		t.Fatal("annual issuance was labeled monthly")
	}
	b.LinkState = "linked"
	if b.ActionURL(now) != "" || strings.Contains(b.Summary(now), "Claim $") {
		t.Fatal("linked offer invited a second claim")
	}
	b.LinkState = "unlinked"
	b.Eligibility = "ineligible"
	if b.Summary(now) != "" || b.ActionURL(now) != "" {
		t.Fatal("ineligible accounts were invited to claim")
	}
}

func TestCreditBenefitUnknownAmountCannotBecomeAClaim(t *testing.T) {
	now := time.Now()
	b := UsageCreditBenefit{Eligibility: "eligible", LinkState: "unlinked", Currency: "USD", ObservedAt: now, ExpiresAt: now.Add(time.Hour), Cadence: "monthly"}
	if strings.Contains(b.Summary(now), "Claim $") {
		t.Fatal("missing amounts turned into a claim")
	}
	amount := int64(1000)
	b.Claimable, b.PeriodCredit = &amount, &amount
	clone := b.Clone()
	*clone.Claimable = 0
	if *b.Claimable != 1000 {
		t.Fatal("clone shares its amount with the source")
	}
}
