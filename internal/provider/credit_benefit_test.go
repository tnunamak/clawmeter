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
	if got := b.Summary(now); got != "Claim $35.00 in API credits ($70.00/month) · offer expires "+expiryTime(b.ExpiresAt, now)+"…" {
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

func TestCreditBenefitLinkedStaleIsUnknown(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	claim, period := int64(3500), int64(7000)
	stale := UsageCreditBenefit{Eligibility: "eligible", LinkState: "linked", LinkObservedAt: now.Add(-6 * time.Minute), Currency: "USD",
		Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	if got := stale.State(now); got != "unknown" {
		t.Fatalf("stale link: state = %s, want unknown", got)
	}
	// An unstamped link is kept, as the offer path keeps an unstamped link.
	unstamped := stale
	unstamped.LinkObservedAt = time.Time{}
	if got := unstamped.State(now); got != "linked" {
		t.Fatalf("unstamped link: state = %s, want linked", got)
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

func TestCreditBenefitSummaryIncludesExpiry(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	claim, period := int64(3500), int64(7000)
	soon := UsageCreditBenefit{Eligibility: "eligible", LinkState: "unlinked", Currency: "USD",
		Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	if got := soon.Summary(now); !strings.Contains(got, "Claim $") || !strings.Contains(got, expiryTime(soon.ExpiresAt, now)) {
		t.Fatalf("claim summary omits the offer expiry: %q", got)
	}
	later := soon
	later.ExpiresAt = now.Add(300 * 24 * time.Hour)
	if soon.Summary(now) == later.Summary(now) {
		t.Fatal("an offer expiring tomorrow reads the same as one expiring in months")
	}
}

func TestCreditBenefitPolledReadingUsesItsOwnHorizon(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	claim, period := int64(20000), int64(20000)
	b := UsageCreditBenefit{Eligibility: "eligible", LinkState: "unlinked", Currency: "USD", Claimable: &claim, PeriodCredit: &period,
		Cadence: "monthly", ObservedAt: now, LinkObservedAt: now, ExpiresAt: now.Add(24 * time.Hour), FreshUntil: now.Add(11 * time.Minute)}
	if b.State(now.Add(10*time.Minute)) != "claimable" {
		t.Fatal("polled offer went stale before its reading age")
	}
	if b.State(now.Add(11*time.Minute)) != "stale" || b.CurrentUntil(now) != now.Add(11*time.Minute) {
		t.Fatal("polled offer outlived its reading age")
	}
	b.LinkState = "linked"
	if b.State(now.Add(10*time.Minute)) != "linked" || b.State(now.Add(11*time.Minute)) != "unknown" {
		t.Fatal("polled link did not age with its reading")
	}
	b.FreshUntil = time.Time{}
	if b.CurrentUntil(now) != now.Add(5*time.Minute) || b.State(now.Add(6*time.Minute)) != "unknown" {
		t.Fatal("browser snapshot lost its five-minute rule")
	}
}

func TestCreditBenefitAgedIneligibilityStaysQuiet(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	b := UsageCreditBenefit{Eligibility: "ineligible", LinkState: "unlinked", ObservedAt: now}
	later := now.Add(time.Hour)
	if b.State(later) != "stale" || b.Summary(later) != "" || b.ActionURL(later) != "" {
		t.Fatal("an old refusal asked the user to check the benefit")
	}
	b.Eligibility = "eligible"
	if b.Summary(later) != "Check API credit benefit…" || b.ActionURL(later) != OpenAIAPIBillingURL {
		t.Fatal("an aged offer lost its check action")
	}
}
