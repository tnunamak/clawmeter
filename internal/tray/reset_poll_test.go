//go:build tray

package tray

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/status"
)

// A reset that passed less than resetPollDelay ago still has its poll ahead, so
// the timer must be armed for it.
func TestResetPollAtKeepsAPollStillAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h", ResetsAt: now.Add(-10 * time.Second)},
		}},
	}
	got, ok := resetPollAt(results, nil, nil, now)
	if want := now.Add(20 * time.Second); !ok || !got.Equal(want) {
		t.Fatalf("resetPollAt = %v, %v; want %v, true", got, ok, want)
	}
}

// The reset poll follows the earliest poll still ahead. A source fetched since a
// poll time has that poll served, so it does not count.
func TestResetPollAtFollowsTheEarliestPollAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h", ResetsAt: now.Add(3 * time.Hour)},
			{Name: "7d", ResetsAt: now.Add(-time.Minute)},
		}},
		"codex": {Windows: []provider.UsageWindow{
			{Name: "5h", ResetsAt: now.Add(time.Hour)},
		}},
		"absent": nil,
	}
	servedAt := map[string]time.Time{"claude": now, "codex": now}
	got, ok := resetPollAt(results, servedAt, nil, now)
	if want := now.Add(time.Hour + resetPollDelay); !ok || !got.Equal(want) {
		t.Fatalf("resetPollAt = %v, %v; want %v, true", got, ok, want)
	}
}

// A source fetched less than resetPollDelay ago waits out the spacing even when its
// poll time is still ahead. Otherwise a reading that reports a just-passed reset
// could make the tray fetch back to back.
func TestResetPollAtSpacesAFuturePollFromTheLastFetch(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-20 * time.Second)}}},
	}
	// The poll time is 10 s ahead, but the last fetch began at now.
	servedAt := map[string]time.Time{"claude": now}
	got, ok := resetPollAt(results, servedAt, nil, now)
	if want := now.Add(resetPollDelay); !ok || !got.Equal(want) {
		t.Fatalf("resetPollAt = %v, %v; want %v, true, the end of the spacing", got, ok, want)
	}
	if due := dueResetSources(results, servedAt, nil, now); len(due) != 0 {
		t.Fatalf("dueResetSources = %v; want none inside the spacing", due)
	}
}

func TestResetPollAtWithEveryPollServed(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h"},
			{Name: "7d", ResetsAt: now.Add(-time.Minute)},
			{Name: "1d", ResetsAt: now.Add(-resetPollDelay)},
		}},
	}
	servedAt := map[string]time.Time{"claude": now}
	if got, ok := resetPollAt(results, servedAt, nil, now); ok {
		t.Fatalf("resetPollAt = %v; want none, since the fetch at now is at or past every poll time", got)
	}
}

func TestDueResetSourcesNamesOnlyPollsThatHaveCome(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"early":   {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-resetPollDelay + time.Second)}}},
		"due":     {Windows: []provider.UsageWindow{{Name: "5h"}, {Name: "7d", ResetsAt: now.Add(-resetPollDelay)}}},
		"absent":  nil,
		"unknown": {Windows: []provider.UsageWindow{{Name: "5h"}}},
	}
	got := dueResetSources(results, nil, nil, now)
	if len(got) != 1 || !got["due"] {
		t.Fatalf("dueResetSources = %v, want only due", got)
	}
}

// A fetch that started at or after a source's poll time read the window after its
// reset, so that source is not due. A fetch that started before the poll time may
// have read the window before its reset, so it is due, as is a source never fetched.
func TestDueResetSourcesSkipsASourceFetchedAfterItsPoll(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	poll := now.Add(-time.Minute + resetPollDelay)
	reset := []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-time.Minute)}}
	results := map[string]*provider.UsageData{
		"served":  {Windows: reset},
		"earlier": {Windows: reset},
		"never":   {Windows: reset},
	}
	servedAt := map[string]time.Time{
		"served":  poll,
		"earlier": poll.Add(-time.Second),
	}
	got := dueResetSources(results, servedAt, nil, now)
	if got["served"] || !got["earlier"] || !got["never"] || len(got) != 2 {
		t.Fatalf("dueResetSources = %v, want earlier and never", got)
	}
}

// A source in failure backoff is left to the failure gate, so the reset poll does
// not name it even when its reset poll has come.
func TestDueResetSourcesSkipsASourceInFailureBackoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-time.Minute)}}},
	}
	gate := provider.NewFailureGate()
	if got := dueResetSources(results, nil, gate, now); !got["claude"] {
		t.Fatalf("dueResetSources = %v before any failure, want claude", got)
	}
	for i := 0; i < 3; i++ {
		gate.ShouldSurfaceError("claude", false)
	}
	if !gate.InBackoff("claude") {
		t.Fatal("three failures did not start a backoff")
	}
	if got := dueResetSources(results, nil, gate, now); len(got) != 0 {
		t.Fatalf("dueResetSources = %v during backoff, want none", got)
	}
}

// A due source is caught up at once when it was never fetched, or was fetched
// long before. When it was fetched less than resetPollDelay ago, the catch-up
// waits until that delay ends, so a reading that keeps reporting a just-passed
// reset cannot make the tray fetch back to back.
func TestResetPollAtCatchesUpAtOnceOrAfterTheSpacing(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-31 * time.Second)}}},
	}
	for _, tc := range []struct {
		name     string
		servedAt time.Time
		want     time.Time
	}{
		{name: "never fetched", want: now},
		{name: "fetched long ago", servedAt: now.Add(-2 * time.Minute), want: now},
		{name: "fetched recently", servedAt: now.Add(-10 * time.Second), want: now.Add(20 * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			servedAt := map[string]time.Time{}
			if !tc.servedAt.IsZero() {
				servedAt["claude"] = tc.servedAt
			}
			got, ok := resetPollAt(results, servedAt, nil, now)
			if !ok || !got.Equal(tc.want) {
				t.Fatalf("resetPollAt = %v, %v; want %v, true", got, ok, tc.want)
			}
		})
	}
}

// A source fetched at its poll time needs no catch-up, and with no poll ahead the
// reset poll is not needed at all.
func TestResetPollAtNeedsNoCatchUpAfterAFetchPastThePoll(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-31 * time.Second)}}},
	}
	servedAt := map[string]time.Time{"claude": now.Add(-time.Second)}
	if got, ok := resetPollAt(results, servedAt, nil, now); ok {
		t.Fatalf("resetPollAt = %v; want none, since the fetch began at the poll time", got)
	}
}

// Each arm gets a new generation. The timer from an earlier arm must not run
// the reset poll once a later arm has replaced it.
func TestScheduleResetPollSupersedesEarlierArm(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(time.Hour)}}},
	}
	r := &usageRefresher{reloadConfig: func() { t.Error("a superseded reset poll refreshed") }}
	t.Cleanup(func() {
		if r.resetPoll != nil {
			r.resetPoll.Stop()
		}
	})
	r.scheduleResetPoll(results, now)
	first := r.resetGen
	r.scheduleResetPoll(results, now)
	if r.resetGen != first+1 || r.resetPoll == nil {
		t.Fatalf("resetGen = %d after re-arm, want %d with a poll armed", r.resetGen, first+1)
	}
	r.fireResetPoll(first)
}

// A poll whose sources are not yet due fetches nothing and re-arms the next one.
func TestResetPollWithNothingDueOnlyRearms(t *testing.T) {
	s.mu.Lock()
	saved := s.lastResults
	s.lastResults = nil
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.lastResults = saved
		s.mu.Unlock()
	})
	r := &usageRefresher{reloadConfig: func() { t.Error("a poll with nothing due fetched") }}
	r.resetGen = 1
	r.fireResetPoll(1)
	if r.resetGen != 2 || r.resetPoll != nil {
		t.Fatalf("resetGen = %d, resetPoll = %v; want re-armed with no poll ahead", r.resetGen, r.resetPoll)
	}
}

// A reset poll fetches only its due sources. Another source keeps its last
// reading, and a source with no reading is still fetched.
func TestLimitToDueSourcesKeepsTheOthersReadings(t *testing.T) {
	last := map[string]*provider.UsageData{
		"due":   {Provider: "due", FetchedAt: time.Now(), Windows: []provider.UsageWindow{{Name: "5h", Utilization: 40}}},
		"other": {Provider: "other", FetchedAt: time.Now(), Windows: []provider.UsageWindow{{Name: "5h", Utilization: 20}}},
	}
	toFetch := []provider.Provider{trayStubProvider{name: "due"}, trayStubProvider{name: "other"}, trayStubProvider{name: "new"}}
	fetch, skipped := limitToDueSources(toFetch, map[string]*provider.UsageData{}, last, map[string]bool{"due": true})
	if got := providerNames(fetch); len(got) != 2 || got[0] != "due" || got[1] != "new" {
		t.Fatalf("fetch = %v, want [due new]", got)
	}
	if _, ok := skipped["due"]; ok {
		t.Fatal("a due source was kept instead of fetched")
	}
	kept := skipped["other"]
	if kept == nil || kept == last["other"] || kept.Windows[0].Utilization != 20 {
		t.Fatalf("skipped[other] = %+v, want a clone of its last reading", kept)
	}
}

// resetPollStub reports one window that resets far ahead and counts its fetches.
// A fetch reports its start on started, and waits on hold when hold is set.
type resetPollStub struct {
	name    string
	fetches *atomic.Int32
	started chan<- struct{}
	hold    <-chan struct{}
}

func (p resetPollStub) Name() string         { return p.name }
func (p resetPollStub) DisplayName() string  { return p.name }
func (p resetPollStub) Description() string  { return "" }
func (p resetPollStub) DashboardURL() string { return "" }
func (p resetPollStub) IsConfigured() bool   { return true }

func (p resetPollStub) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	p.fetches.Add(1)
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	if p.hold != nil {
		select {
		case <-p.hold:
		case <-ctx.Done():
		}
	}
	return &provider.UsageData{
		Provider:  p.name,
		FetchedAt: time.Now(),
		Windows:   []provider.UsageWindow{{Name: "5h", ResetsAt: time.Now().Add(4 * time.Hour)}},
	}, nil
}

// fakeClock is a time source that a test moves by hand, so that a test decides
// exactly which poll times have passed, and when.
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock(at time.Time) *fakeClock {
	c := &fakeClock{}
	c.set(at)
	return c
}

func (c *fakeClock) set(at time.Time) { c.ns.Store(at.UnixNano()) }

func (c *fakeClock) now() time.Time { return time.Unix(0, c.ns.Load()) }

// newResetPollFixture returns a refresher over the stub sources, starting from
// lastResults, that reads time from clock. The shared refresh state, the config and
// the refresh clock are restored when the test ends.
func newResetPollFixture(t *testing.T, clock *fakeClock, lastResults map[string]*provider.UsageData, stubs ...resetPollStub) *usageRefresher {
	t.Helper()
	oldCfg := cfg
	cfg = config.DefaultConfig()
	s.mu.Lock()
	savedResults, savedRevisions, savedGate := s.lastResults, s.lastSourceRevisions, s.failureGate
	savedRefresh, savedFeed := s.lastRefreshAt, s.feedReading
	s.lastResults, s.lastSourceRevisions, s.failureGate = lastResults, nil, provider.NewFailureGate()
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.lastResults, s.lastSourceRevisions, s.failureGate = savedResults, savedRevisions, savedGate
		s.lastRefreshAt, s.feedReading = savedRefresh, savedFeed
		s.mu.Unlock()
		cfg = oldCfg
	})
	registry := provider.NewRegistry()
	for _, p := range stubs {
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	r := &usageRefresher{
		registry:     registry,
		reloadConfig: func() {},
		pollSeconds:  func() int { return 300 },
		render:       func(map[string]*provider.UsageData, map[string]*status.ProviderStatus) {},
		finished:     func(time.Time) {},
		resetGen:     1,
	}
	oldClock := refreshClock
	refreshClock = clock.now
	// Cleanups run last-in first-out, so a cleanup a test registers after this one
	// runs first. Each test that holds a fetch open must release it there, before
	// the timer cleanup below takes r.mu. That cleanup restores the clock under
	// r.mu, so no timer still waiting on the lock reads a stale clock.
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.resetPoll != nil {
			r.resetPoll.Stop()
			r.resetPoll = nil
		}
		r.resetGen++ // a timer that fires from here on finds a stale generation
		refreshClock = oldClock
	})
	return r
}

// A reset that passes while another source's refresh is running must not be lost.
// alpha's reset poll starts a refresh that alpha holds open. While it is held, the
// clock passes beta's poll time. When alpha's refresh ends, beta has not been
// fetched since that poll time, so the tray must fetch beta again, once.
func TestResetPollCatchesUpAResetThatPassedDuringAnotherSourcesRefresh(t *testing.T) {
	t0 := time.Now().Round(0)
	clock := newFakeClock(t0)
	// alpha's poll is due now. beta's poll is 10 s ahead, so the reset poll that
	// alpha's reset starts does not fetch beta.
	betaPoll := t0.Add(10 * time.Second)
	var alphaFetches, betaFetches atomic.Int32
	alphaStarted := make(chan struct{}, 1)
	betaStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	r := newResetPollFixture(t, clock, map[string]*provider.UsageData{
		"alpha": {Provider: "alpha", FetchedAt: t0.Add(-time.Minute), Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: t0.Add(-31 * time.Second)}}},
		"beta":  {Provider: "beta", FetchedAt: t0.Add(-time.Minute), Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: betaPoll.Add(-resetPollDelay)}}},
	},
		resetPollStub{name: "alpha", fetches: &alphaFetches, started: alphaStarted, hold: release},
		resetPollStub{name: "beta", fetches: &betaFetches, started: betaStarted},
	)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	fired := make(chan struct{})
	go func() {
		defer close(fired)
		r.fireResetPoll(1)
	}()
	select {
	case <-alphaStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("alpha's reset poll did not start a refresh")
	}
	if n := betaFetches.Load(); n != 0 {
		t.Fatalf("beta was fetched %d times before its poll time, want 0", n)
	}

	// Move the clock past beta's poll time while alpha's refresh is held, then end it.
	clock.set(betaPoll.Add(time.Second))
	close(release)
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("alpha's refresh did not end")
	}
	select {
	case <-betaStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("beta's reset passed during alpha's refresh, and beta was not fetched again")
	}
	r.mu.Lock() // the catch-up refresh holds r.mu until it ends
	r.mu.Unlock()
	if a, b := alphaFetches.Load(), betaFetches.Load(); a != 1 || b != 1 {
		t.Fatalf("fetches: alpha %d, beta %d; want 1 and 1", a, b)
	}

	// Nothing is owed after the catch-up, so a poll at the same time fetches nothing.
	r.mu.Lock()
	gen := r.resetGen
	r.mu.Unlock()
	r.fireResetPoll(gen)
	if a, b := alphaFetches.Load(), betaFetches.Load(); a != 1 || b != 1 {
		t.Fatalf("a poll after the catch-up fetched again: alpha %d, beta %d; want 1 and 1", a, b)
	}
}

// A poll that fires for one source must not fetch another source still inside its
// spacing. beta's poll time has passed, but beta was fetched 5 s ago, so the poll
// fetches only alpha. beta is fetched once its spacing has ended.
func TestResetPollFiresOnlyTheSourcesPastTheirSpacing(t *testing.T) {
	t0 := time.Now().Round(0)
	clock := newFakeClock(t0)
	var alphaFetches, betaFetches atomic.Int32
	r := newResetPollFixture(t, clock, map[string]*provider.UsageData{
		"alpha": {Provider: "alpha", FetchedAt: t0.Add(-time.Minute), Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: t0.Add(-31 * time.Second)}}},
		"beta":  {Provider: "beta", FetchedAt: t0.Add(-time.Minute), Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: t0.Add(-31 * time.Second)}}},
	},
		resetPollStub{name: "alpha", fetches: &alphaFetches},
		resetPollStub{name: "beta", fetches: &betaFetches},
	)
	r.servedAt = map[string]time.Time{"beta": t0.Add(-5 * time.Second)}

	r.fireResetPoll(1)
	if a, b := alphaFetches.Load(), betaFetches.Load(); a != 1 || b != 0 {
		t.Fatalf("fetches: alpha %d, beta %d; want 1 and 0, since beta is inside its spacing", a, b)
	}

	// The next owed poll is beta's spacing end, 25 s after t0.
	r.mu.Lock()
	s.mu.Lock()
	at, ok := resetPollAt(s.lastResults, r.servedAt, s.failureGate, t0)
	s.mu.Unlock()
	r.mu.Unlock()
	if want := t0.Add(resetPollDelay - 5*time.Second); !ok || !at.Equal(want) {
		t.Fatalf("resetPollAt = %v, %v; want %v, true, when beta's spacing ends", at, ok, want)
	}

	// At that time the poll fetches beta, once.
	clock.set(t0.Add(resetPollDelay - 5*time.Second))
	r.mu.Lock()
	gen := r.resetGen
	if r.resetPoll != nil {
		r.resetPoll.Stop()
	}
	r.mu.Unlock()
	r.fireResetPoll(gen)
	if a, b := alphaFetches.Load(), betaFetches.Load(); a != 1 || b != 1 {
		t.Fatalf("after beta's spacing ended: alpha %d, beta %d; want 1 and 1", a, b)
	}
}

// The reset poll compares a source's last fetch with its poll time, so a refresh
// stamps each fetch with the time it started. A fetch that ends after a poll time
// may have read the window before its reset.
func TestRunStampsAFetchWithItsStart(t *testing.T) {
	t0 := time.Now().Round(0)
	clock := newFakeClock(t0)
	var fetches atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	r := newResetPollFixture(t, clock, nil, resetPollStub{name: "alpha", fetches: &fetches, started: started, hold: release})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.run(false, nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("alpha's fetch did not start")
	}
	clock.set(t0.Add(50 * time.Millisecond)) // the fetch ends 50 ms after it began
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh did not end")
	}
	r.mu.Lock()
	served, ok := r.servedAt["alpha"]
	r.mu.Unlock()
	if !ok || !served.Equal(t0) {
		t.Fatalf("alpha's fetch stamped %v (recorded %v), want %v, when it began", served, ok, t0)
	}
}
