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
	grant := buildCreditGrant(provider.UsageCreditGrant{Name: "cloud_sessions", DisplayName: "Cloud session credits", Currency: "USD", Utilization: &pct}, Reading{State: ReadingFresh}, now)
	if grant.Remaining != nil || grant.Limit != nil || grant.State != "unknown" || !strings.Contains(grant.Summary(), "amount not reported") || strings.Contains(grant.Summary(), "$250") {
		t.Fatalf("unknown amounts = %+v; %s", grant, grant.Summary())
	}
}
