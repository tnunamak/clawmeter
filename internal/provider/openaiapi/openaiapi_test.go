package openaiapi

import (
	"context"
	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"strings"
	"testing"
	"time"
)

func TestReceiptSeparatesFundedBalanceFromBenefitAndLinkTimes(t *testing.T) {
	now := time.Now().UTC()
	yes := true
	claim := int64(3500)
	s := &openaicredits.Store{Dir: t.TempDir()}
	r := openaicredits.Snapshot{Pool: strings.Repeat("a", 64), Name: "Synthetic org", Credits: &openaicredits.Credits{Currency: "USD", Balance: 125, ObservedAt: now, Grants: []openaicredits.Grant{}}, Benefit: &openaicredits.Benefit{Eligible: &yes, Currency: "USD", Cadence: "monthly", PeriodCredit: 7000, ClaimableCredit: &claim, ExpiresAt: now.Add(time.Hour), ObservedAt: now, LinkState: "unknown"}, Link: &openaicredits.Link{State: "unlinked", ObservedAt: now}}
	if err := s.Save(r, now); err != nil {
		t.Fatal(err)
	}
	p := &Provider{Store: s}
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 0 || len(data.CreditPools) != 0 || data.APICreditBalances[0].Balance != 125 || data.CreditBenefits[0].State(now) != "claimable" {
		t.Fatalf("mixed offer with funds or quota: %+v", data)
	}
	later := now.Add(time.Minute)
	if err := s.Save(openaicredits.Snapshot{Pool: r.Pool, Name: r.Name, Link: &openaicredits.Link{State: "linked", ObservedAt: later}}, later); err != nil {
		t.Fatal(err)
	}
	data, err = p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !data.CreditBenefits[0].ObservedAt.Equal(now) || data.CreditBenefits[0].State(later) != "linked" {
		t.Fatal("new link revived old monetary offer")
	}
	if data.APICreditBalances[0].State(now.Add(6*time.Minute)) != "stale" {
		t.Fatal("provider poll freshened historical money")
	}
	if data.Clone().APICreditBalances[0].Balance != 125 {
		t.Fatal("clone lost money")
	}
}
