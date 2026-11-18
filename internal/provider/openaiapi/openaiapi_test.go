package openaiapi

import (
	"context"
	"errors"

	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"github.com/tnunamak/clawmeter/internal/provider"
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

type fakeCodex struct {
	key      string
	benefits []provider.UsageCreditBenefit
	err      error
	reads    int
}

func (f *fakeCodex) Name() string                                            { return "openai" }
func (f *fakeCodex) DisplayName() string                                     { return "Codex" }
func (f *fakeCodex) Description() string                                     { return "" }
func (f *fakeCodex) DashboardURL() string                                    { return "" }
func (f *fakeCodex) IsConfigured() bool                                      { return true }
func (f *fakeCodex) SourceID() string                                        { return f.key }
func (f *fakeCodex) SourceRevision() string                                  { return "login-" + f.key }
func (f *fakeCodex) HasChatGPTAuth() bool                                    { return true }
func (f *fakeCodex) FetchUsage(context.Context) (*provider.UsageData, error) { return nil, nil }
func (f *fakeCodex) ReadAPICreditBenefits(context.Context) ([]provider.UsageCreditBenefit, error) {
	f.reads++
	out := make([]provider.UsageCreditBenefit, len(f.benefits))
	for i, b := range f.benefits {
		out[i] = b.Clone()
	}
	return out, f.err
}

func polledOffer(pool string, observed time.Time) provider.UsageCreditBenefit {
	claim, period := int64(20000), int64(20000)
	return provider.UsageCreditBenefit{Name: "Personal", Pool: pool, Eligibility: "eligible", LinkState: "unlinked", Currency: "USD",
		Claimable: &claim, PeriodCredit: &period, Cadence: "monthly", ExpiresAt: observed.Add(20 * 24 * time.Hour),
		ObservedAt: observed, LinkObservedAt: observed.Add(time.Second)}
}

func TestPolledBenefitNeedsNoExtensionAndStaysCurrentBetweenPolls(t *testing.T) {
	now := time.Now().UTC()
	pool := strings.Repeat("b", 64)
	codex := &fakeCodex{key: "default", benefits: []provider.UsageCreditBenefit{polledOffer(pool, now)}}
	p := New([]provider.Provider{codex}, 11*time.Minute)
	p.Store = &openaicredits.Store{Dir: t.TempDir()}
	if !p.IsConfigured() {
		t.Fatal("a ChatGPT login alone must configure the benefit")
	}
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.CreditBenefits) != 1 || len(data.APICreditBalances) != 0 {
		t.Fatalf("data = %+v", data)
	}
	b := data.CreditBenefits[0]
	if !b.FreshUntil.Equal(now.Add(11 * time.Minute)) {
		t.Fatalf("fresh until = %v, want the older observation plus the reading age", b.FreshUntil)
	}
	// The tray polls every five minutes; the claim must survive until the next poll.
	if b.State(now.Add(6*time.Minute)) != "claimable" || !strings.HasPrefix(b.Summary(now.Add(6*time.Minute)), "Personal: Claim $200.00") {
		t.Fatalf("polled claim went stale between polls: %s", b.Summary(now.Add(6*time.Minute)))
	}
	if b.State(now.Add(11*time.Minute)) != "stale" || strings.Contains(b.Summary(now.Add(11*time.Minute)), "$") {
		t.Fatal("polled claim outlived its reading age")
	}
	if !data.HasPresentableUsage() {
		t.Fatal("eligible offer not presentable")
	}
}

func TestPolledBenefitSupersedesSnapshotButKeepsFundedBalance(t *testing.T) {
	now := time.Now().UTC()
	yes := true
	claim := int64(3500)
	pool := strings.Repeat("c", 64)
	store := &openaicredits.Store{Dir: t.TempDir()}
	snapshot := openaicredits.Snapshot{Pool: pool, Name: "Personal",
		Credits: &openaicredits.Credits{Currency: "USD", Balance: 4200, ObservedAt: now, Grants: []openaicredits.Grant{}},
		Benefit: &openaicredits.Benefit{Eligible: &yes, Currency: "USD", Cadence: "monthly", PeriodCredit: 7000, ClaimableCredit: &claim, ExpiresAt: now.Add(time.Hour), ObservedAt: now, LinkState: "unknown"}}
	if err := store.Save(snapshot, now); err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("d", 64)
	if err := store.Save(openaicredits.Snapshot{Pool: other, Name: "Old org", Benefit: snapshot.Benefit}, now); err != nil {
		t.Fatal(err)
	}
	codex := &fakeCodex{key: "default", benefits: []provider.UsageCreditBenefit{polledOffer(pool, now.Add(time.Second))}}
	p := New([]provider.Provider{codex}, 11*time.Minute)
	p.Store = store
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.APICreditBalances) != 1 || data.APICreditBalances[0].Balance != 4200 {
		t.Fatalf("funded balance from the extension was lost: %+v", data.APICreditBalances)
	}
	byPool := map[string]provider.UsageCreditBenefit{}
	for _, b := range data.CreditBenefits {
		if _, dup := byPool[b.Pool]; dup {
			t.Fatalf("pool %s shown twice", b.Pool)
		}
		byPool[b.Pool] = b
	}
	if len(byPool) != 2 || *byPool[pool].PeriodCredit != 20000 || byPool[pool].FreshUntil.IsZero() {
		t.Fatalf("polled reading did not supersede the snapshot: %+v", byPool[pool])
	}
	if *byPool[other].PeriodCredit != 7000 || !byPool[other].FreshUntil.IsZero() {
		t.Fatal("snapshot of an organization the poll did not see changed its meaning")
	}
}

func TestFailedPollFallsBackToSavedReadings(t *testing.T) {
	now := time.Now().UTC()
	store := &openaicredits.Store{Dir: t.TempDir()}
	pool := strings.Repeat("e", 64)
	if err := store.Save(openaicredits.Snapshot{Pool: pool, Name: "Personal", Credits: &openaicredits.Credits{Currency: "USD", Balance: 10, ObservedAt: now, Grants: []openaicredits.Grant{}}}, now); err != nil {
		t.Fatal(err)
	}
	codex := &fakeCodex{key: "default", err: errors.New("http 403")}
	p := New([]provider.Provider{codex}, 11*time.Minute)
	p.Store = store
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.APICreditBalances) != 1 {
		t.Fatalf("saved balance hidden by a failed poll: %+v, %v", data, err)
	}
	p.Store = &openaicredits.Store{Dir: t.TempDir()}
	if _, err := p.FetchUsage(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("failed poll with nothing saved: err = %v", err)
	}
}

func TestTwoLoginsShowOneOrganizationOnce(t *testing.T) {
	now := time.Now().UTC()
	pool := strings.Repeat("f", 64)
	first := &fakeCodex{key: "a", benefits: []provider.UsageCreditBenefit{polledOffer(pool, now)}}
	second := &fakeCodex{key: "b", benefits: []provider.UsageCreditBenefit{polledOffer(pool, now)}}
	p := New([]provider.Provider{first, second}, 11*time.Minute)
	p.Store = &openaicredits.Store{Dir: t.TempDir()}
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.CreditBenefits) != 1 {
		t.Fatalf("benefits = %+v, %v", data, err)
	}
	before := p.SourceRevision()
	second.key = "c"
	if p.SourceRevision() == before {
		t.Fatal("a changed Codex login kept the cached benefit")
	}
}

func TestTwoLoginsKeepTheEligibleOfferAndTheNewestLink(t *testing.T) {
	now := time.Now().UTC()
	pool := strings.Repeat("1", 64)
	eligible := polledOffer(pool, now)
	refused := provider.UsageCreditBenefit{Name: "Personal", Pool: pool, Eligibility: "ineligible", LinkState: "unlinked", ObservedAt: now, LinkObservedAt: now}
	p := New([]provider.Provider{&fakeCodex{key: "a", benefits: []provider.UsageCreditBenefit{refused}}, &fakeCodex{key: "b", benefits: []provider.UsageCreditBenefit{eligible}}}, 11*time.Minute)
	p.Store = &openaicredits.Store{Dir: t.TempDir()}
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.CreditBenefits) != 1 || data.CreditBenefits[0].State(now.Add(2*time.Second)) != "claimable" {
		t.Fatalf("another account's refusal hid an eligible offer: %+v, %v", data, err)
	}
	linked := refused
	linked.LinkState, linked.LinkObservedAt = "linked", now.Add(2*time.Second)
	p = New([]provider.Provider{&fakeCodex{key: "a", benefits: []provider.UsageCreditBenefit{eligible}}, &fakeCodex{key: "b", benefits: []provider.UsageCreditBenefit{linked}}}, 11*time.Minute)
	p.Store = &openaicredits.Store{Dir: t.TempDir()}
	data, err = p.FetchUsage(context.Background())
	if err != nil || len(data.CreditBenefits) != 1 {
		t.Fatalf("benefits = %+v, %v", data, err)
	}
	b := data.CreditBenefits[0]
	if b.State(now.Add(3*time.Second)) != "linked" || strings.Contains(b.Summary(now.Add(3*time.Second)), "Claim") {
		t.Fatal("a newer linked reading from a second login left the claim up")
	}
}
