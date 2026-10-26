package view

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestCreditGrantStatesNeverCreateAResetOrSpendableOldBalance(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	used, limit := 7.125, 250.0
	for _, tc := range []struct {
		name, state, words string
		stale              bool
		expires            time.Time
	}{
		{"available", "available", "$242.88 of $250.00 left", false, now.Add(time.Hour)},
		{"stale", "stale", "last known $242.88", true, now.Add(time.Hour)},
		{"expiry boundary", "expired", "$242.88 left at last check", false, now},
		{"expired offline", "expired", "expired", true, now.Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := &provider.UsageData{Provider: "claude", FetchedAt: now, Stale: tc.stale,
				CreditGrants: []provider.UsageCreditGrant{{Name: "cloud_sessions", DisplayName: "Cloud session credits", Scope: "cloud_sessions", Currency: "USD", Used: &used, Limit: &limit, ExpiresAt: tc.expires}},
			}
			snap := Build(Inputs{Results: map[string]*provider.UsageData{"claude": data}}, now)
			acct := snap.Accounts[0]
			if snap.Selected != nil || snap.Worst != nil || len(acct.Windows) != 0 || len(acct.Rows()) != 0 {
				t.Fatal("grant became a ranked or pinnable quota")
			}
			grant := acct.CreditGrants[0]
			if grant.State != tc.state || !strings.Contains(grant.Summary(), tc.words) || strings.Contains(grant.Summary(), "reset") {
				t.Fatalf("grant = %+v, summary=%s", grant, grant.Summary())
			}
			if tc.state != "available" && (grant.Remaining != nil || grant.LastObservedRemaining == nil) {
				t.Fatal("old balance was advertised as currently remaining")
			}
			*grant.Used = 90
			if used != 7.125 {
				t.Fatal("snapshot grant aliases provider observation")
			}
		})
	}
}

func TestCreditGrantMissingAmountsRemainUnknown(t *testing.T) {
	pct := 1.0
	now := time.Now()
	grant := buildCreditGrant(provider.UsageCreditGrant{Name: "cloud_sessions", DisplayName: "Cloud session credits", Currency: "USD", Utilization: &pct}, Reading{State: ReadingFresh, ObservedAt: now}, now, time.Minute)
	if grant.Remaining != nil || grant.Limit != nil || grant.State != "unknown" || !strings.Contains(grant.Summary(), "amount not reported") || strings.Contains(grant.Summary(), "$250") {
		t.Fatalf("unknown amounts = %+v; %s", grant, grant.Summary())
	}
}

func TestCreditGrantRequiresATrustworthyObservationTime(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	used, limit := 7.125, 250.0
	maxAge := 2 * time.Minute
	for _, tc := range []struct {
		name     string
		observed time.Time
		ageLimit time.Duration
		current  bool
	}{
		{"current", now, maxAge, true},
		{"age boundary", now.Add(-maxAge), maxAge, true},
		{"past age boundary", now.Add(-maxAge - time.Nanosecond), maxAge, false},
		{"missing", time.Time{}, maxAge, false},
		{"future", now.Add(time.Nanosecond), maxAge, false},
		{"zero age ceiling", now.Add(-time.Nanosecond), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := &provider.UsageData{Provider: "claude", FetchedAt: tc.observed, CreditGrants: []provider.UsageCreditGrant{{Name: "cloud_sessions", DisplayName: "Cloud session credits", Currency: "USD", Used: &used, Limit: &limit, ExpiresAt: now.Add(time.Hour)}}}
			snap := Build(Inputs{Results: map[string]*provider.UsageData{"claude": data}, MaxReadingAge: tc.ageLimit}, now)
			acct := snap.Accounts[0]
			grant := acct.CreditGrants[0]
			if (grant.Remaining != nil) != tc.current {
				t.Fatalf("observation %v exposed current balance: %+v", tc.observed, grant)
			}
			if !tc.current && (grant.State != "stale" || grant.LastObservedRemaining == nil || !strings.Contains(grant.Summary(), "last known")) {
				t.Fatalf("untrusted observation lost its historical balance: %+v", grant)
			}
			if acct.Reading.State != ReadingFresh || snap.Selected != nil {
				t.Fatal("grant freshness changed the account/quota state")
			}
		})
	}
}
