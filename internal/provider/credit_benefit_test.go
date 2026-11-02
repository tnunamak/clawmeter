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
	b.LinkState, b.LinkObservedAt = "linked", now
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

func TestCreditBenefitLinkedStaleIsUnknown(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	claim, period := int64(3500), int64(7000)
	for _, linkAt := range []time.Time{{}, now.Add(-6 * time.Minute)} {
		b := UsageCreditBenefit{Eligibility: "eligible", LinkState: "linked", LinkObservedAt: linkAt, Currency: "USD",
			Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
		if got := b.State(now); got != "unknown" {
			t.Fatalf("link observed at %v: state = %s, want unknown", linkAt, got)
		}
	}
	fresh := UsageCreditBenefit{LinkState: "linked", LinkObservedAt: now.Add(-time.Minute)}
	if got := fresh.State(now); got != "linked" {
		t.Fatalf("fresh link: state = %s, want linked", got)
	}
}

func TestCreditBenefitFreshOfferBeatsStaleLink(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	claim, period := int64(3500), int64(7000)
	b := UsageCreditBenefit{Eligibility: "eligible", LinkState: "linked", LinkObservedAt: now.Add(-10 * time.Minute),
		Currency: "USD", Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	if got := b.State(now); got == "linked" {
		t.Fatal("stale link hid a fresh offer behind the linked state")
	}
	if b.ActionURL(now) != OpenAIAPIBillingURL {
		t.Fatal("fresh offer with a stale link has no review action")
	}
	if b.Claimable == nil || *b.Claimable != claim {
		t.Fatal("claimable amount was dropped")
	}
}
