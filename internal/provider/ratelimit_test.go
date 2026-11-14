package provider

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/filelock"
)

// TestMain keeps every test in this package away from the user's real backoff
// state: a real 429 recorded for "claude" would otherwise gate test fetches.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "clawmeter-backoff-test-")
	if err != nil {
		panic(err)
	}
	backoffDir = func() (string, error) { return dir, nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// limitedProvider answers every fetch with err (or a clean reading if nil) and
// counts how often it was actually called.
type limitedProvider struct {
	name, id string
	err      error
	calls    atomic.Int32
}

func (p *limitedProvider) Name() string         { return p.name }
func (p *limitedProvider) DisplayName() string  { return p.name }
func (p *limitedProvider) Description() string  { return "test" }
func (p *limitedProvider) DashboardURL() string { return "" }
func (p *limitedProvider) IsConfigured() bool   { return true }
func (p *limitedProvider) SourceID() string     { return p.id }
func (p *limitedProvider) SourceLabel() string  { return p.id }
func (p *limitedProvider) FetchUsage(context.Context) (*UsageData, error) {
	p.calls.Add(1)
	if p.err != nil {
		return nil, p.err
	}
	return &UsageData{Windows: []UsageWindow{{Name: "5h", Utilization: 10}}}, nil
}

// isolateBackoff gives the test its own backoff dir, a controllable clock, and cooldown IDs
// that count up from 1, so each recorded cooldown's ID is known and nonzero.
func isolateBackoff(t *testing.T) *time.Time {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldNow, oldID := backoffDir, backoffNow, newBackoffID
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	var lastID uint64
	backoffDir = func() (string, error) { return dir, nil }
	backoffNow = func() time.Time { return clock }
	newBackoffID = func() uint64 {
		lastID++
		return lastID
	}
	resetUnwritten()
	t.Cleanup(func() {
		backoffDir, backoffNow, newBackoffID = oldDir, oldNow, oldID
		resetUnwritten()
	})
	return &clock
}

func fetchExpectingCalls(t *testing.T, p *limitedProvider, want int32, when string) *UsageData {
	t.Helper()
	data, err := FetchSource(context.Background(), p)
	if err != nil {
		t.Fatalf("%s: err = %v; a rate limit must be reported as data, not an error", when, err)
	}
	if got := p.calls.Load(); got != want {
		t.Fatalf("%s: provider calls = %d, want %d", when, got, want)
	}
	return data
}

func TestRateLimitBacksOffAndHonorsFloor(t *testing.T) {
	clock := isolateBackoff(t)
	p := &limitedProvider{name: "claude", id: "work", err: &RateLimitError{}}

	data := fetchExpectingCalls(t, p, 1, "first 429")
	if !strings.HasPrefix(data.Error, "rate limited (429), next try ") || data.Provider != "claude" || data.SourceID != "work" {
		t.Fatalf("rate-limited data = %#v", data)
	}
	*clock = clock.Add(4 * time.Minute)
	if data := fetchExpectingCalls(t, p, 1, "inside 5m floor"); !strings.Contains(data.Error, "rate limited") {
		t.Fatalf("backed-off data = %#v", data)
	}
	*clock = clock.Add(time.Minute + time.Second) // 5m1s after the first 429, past the floor
	fetchExpectingCalls(t, p, 2, "after floor")
	// Second consecutive 429 doubles the delay to 10 minutes.
	*clock = clock.Add(10*time.Minute - time.Second)
	fetchExpectingCalls(t, p, 2, "inside doubled 10m backoff")
	*clock = clock.Add(time.Second)
	fetchExpectingCalls(t, p, 3, "after doubled backoff")
}

func TestRateLimitBackoffCapsAtOneHour(t *testing.T) {
	clock := isolateBackoff(t)
	key := "claude:work"
	var until time.Time
	for i := 0; i < 8; i++ {
		until = recordRateLimit(key, 0)
	}
	if got := until.Sub(*clock); got != backoffCap {
		t.Fatalf("delay after 8 consecutive 429s = %v, want %v", got, backoffCap)
	}
}

// A server's Retry-After is its own cooldown, and the local cap does not shorten
// it. A monthly quota can send days, and retrying before its reset is refused.
func TestRateLimitHonorsRetryAfterInFull(t *testing.T) {
	for _, tc := range []struct {
		retryAfter, want time.Duration
	}{
		{0, backoffFloor},
		{30 * time.Second, backoffFloor},
		{30 * time.Minute, 30 * time.Minute},
		{2 * time.Hour, 2 * time.Hour},
		{30 * time.Hour, 30 * time.Hour},
		{48 * time.Hour, 48 * time.Hour},
		{30 * 24 * time.Hour, 30 * 24 * time.Hour},
	} {
		clock := isolateBackoff(t)
		if got := recordRateLimit("claude:work", tc.retryAfter).Sub(*clock); got != tc.want {
			t.Errorf("retry-after %v: delay = %v, want %v", tc.retryAfter, got, tc.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	clock := isolateBackoff(t)
	for value, want := range map[string]time.Duration{
		"":                      0,
		"0":                     0,
		"-5":                    0,
		"120":                   2 * time.Minute,
		" 60":                   time.Minute,
		"soon":                  0,
		"86400":                 24 * time.Hour,
		"172800":                48 * time.Hour,
		"99999999999999999999":  time.Duration(math.MaxInt64/int64(time.Second)) * time.Second,
		"-99999999999999999999": 0,
		clock.Add(20 * time.Minute).Format(http.TimeFormat): 20 * time.Minute,
		clock.Add(48 * time.Hour).Format(http.TimeFormat):   48 * time.Hour,
		clock.Add(-time.Minute).Format(http.TimeFormat):     0,
	} {
		if got := parseRetryAfter(value); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestRateLimitFromResponse(t *testing.T) {
	if err := RateLimitFromResponse(&http.Response{StatusCode: http.StatusInternalServerError}); err != nil {
		t.Fatalf("500 reported as rate limit: %v", err)
	}
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"1800"}}}
	limited, ok := RateLimitFromResponse(resp).(*RateLimitError)
	if !ok || limited.RetryAfter != 30*time.Minute {
		t.Fatalf("429 = %#v", limited)
	}
}

func TestSuccessClearsBackoff(t *testing.T) {
	clock := isolateBackoff(t)
	p := &limitedProvider{name: "claude", id: "work"}
	recordRateLimit(SourceKey(p), 0)
	if _, ok := readBackoff(backoffKey(p)); !ok {
		t.Fatal("backoff not recorded")
	}
	*clock = clock.Add(10 * time.Minute)
	if data := fetchExpectingCalls(t, p, 1, "after backoff"); data.Error != "" {
		t.Fatalf("fetch after backoff: %q", data.Error)
	}
	*clock = clock.Add(-10 * time.Minute)
	if _, ok := readBackoff(backoffKey(p)); ok {
		t.Fatal("successful fetch did not clear backoff")
	}
	if state, ok := readBackoff(backoffKey(p)); ok {
		t.Fatalf("failure count survived success: %#v", state)
	}
}

func TestOtherErrorsNeitherStartNorClearBackoff(t *testing.T) {
	isolateBackoff(t)
	failing := &limitedProvider{name: "zai", err: fmt.Errorf("API returned 500")}
	if _, err := FetchSource(context.Background(), failing); err == nil {
		t.Fatal("non-429 error was swallowed")
	}
	if _, ok := readBackoff(backoffKey(failing)); ok {
		t.Fatal("non-429 error started a backoff")
	}
}

func TestRateLimitBackoffIsPerSource(t *testing.T) {
	isolateBackoff(t)
	limited := &limitedProvider{name: "claude", id: "work", err: &RateLimitError{}}
	other := &limitedProvider{name: "claude", id: "personal"}
	FetchSource(context.Background(), limited)
	fetchExpectingCalls(t, other, 1, "other source of same provider")
	if data := fetchExpectingCalls(t, other, 2, "other source again"); data.Error != "" {
		t.Fatalf("unrelated source was backed off: %q", data.Error)
	}
}

// Wrapped errors still count: providers may add context with %w.
func TestWrappedRateLimitErrorIsRecognized(t *testing.T) {
	isolateBackoff(t)
	p := &limitedProvider{name: "openrouter", err: fmt.Errorf("fetch credits: %w", &RateLimitError{})}
	FetchSource(context.Background(), p)
	fetchExpectingCalls(t, p, 1, "after wrapped 429")
}

func TestConcurrentRateLimitsKeepEverySourcesBackoff(t *testing.T) {
	isolateBackoff(t)
	providers := make([]Provider, 16)
	for i := range providers {
		providers[i] = &limitedProvider{name: "claude", id: fmt.Sprintf("s%d", i), err: &RateLimitError{}}
	}
	result := FetchProvidersParallel(context.Background(), providers)
	for _, p := range providers {
		key := SourceKey(p)
		if data := result.Results[key]; data == nil || !strings.Contains(data.Error, "rate limited") || result.Errors[key] != nil {
			t.Fatalf("source %s result = %#v, err = %v", key, data, result.Errors[key])
		}
		if _, ok := readBackoff(key); !ok {
			t.Fatalf("source %s lost its backoff after concurrent 429s", key)
		}
	}
	FetchProvidersParallel(context.Background(), providers)
	for _, p := range providers {
		if calls := p.(*limitedProvider).calls.Load(); calls != 1 {
			t.Fatalf("source %s called %d times, want 1 (second round backed off)", SourceKey(p), calls)
		}
	}
}

type partialProvider struct{ limitedProvider }

func (p *partialProvider) FetchUsage(context.Context) (*UsageData, error) {
	p.calls.Add(1)
	return &UsageData{Windows: []UsageWindow{{Name: "key", Utilization: 50}}, Warning: "wallet unavailable"}, &RateLimitError{}
}

func TestPartialReadingWithRateLimitIsKeptAndBacksOff(t *testing.T) {
	isolateBackoff(t)
	p := &partialProvider{limitedProvider{name: "openrouter"}}
	data, err := FetchSource(context.Background(), p)
	if err != nil || data == nil || len(data.Windows) != 1 || data.Error != "" {
		t.Fatalf("partial reading = %#v, %v; want it kept", data, err)
	}
	if _, ok := readBackoff(backoffKey(p)); !ok {
		t.Fatal("partial reading with a 429 did not start a backoff")
	}
	fetchExpectingCalls(t, &p.limitedProvider, 1, "after partial 429")
}

// Overlapping writers for one source must not drop each other's cooldown. Each
// recordRateLimit reads the stored state, adds its own cooldown and writes it
// back. Without a lock around that read-modify-write, a writer that read before
// the longest cooldown was stored would overwrite it with a shorter one. This is a
// stress test: it can pass with a race still present. The busy-lock and ID tests at
// the end of this file are the deterministic checks.
func TestOverlappingRecordsKeepTheLongestCooldown(t *testing.T) {
	clock := isolateBackoff(t)
	const longest = 3 * time.Hour
	for round := 0; round < 20; round++ {
		key := fmt.Sprintf("claude:round-%d", round)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 32; i++ {
			retryAfter := time.Duration(i+1) * 5 * time.Minute
			if i == 17 {
				retryAfter = longest
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				recordRateLimit(key, retryAfter)
			}()
		}
		close(start)
		wg.Wait()
		state, ok := readBackoff(key)
		if want := clock.Add(longest); !ok || !state.Until.Equal(want) {
			t.Fatalf("round %d: stored cooldown ends %v (found %v), want %v: an overlapping write dropped the longest one", round, state.Until, ok, want)
		}
	}
}

// gatedProvider's fetch starts and then waits for release, so a test can write to
// the backoff state while the request is in flight.
type gatedProvider struct {
	limitedProvider
	started, release chan struct{}
}

func (p *gatedProvider) FetchUsage(context.Context) (*UsageData, error) {
	close(p.started)
	<-p.release
	return &UsageData{Windows: []UsageWindow{{Name: "5h", Utilization: 10}}}, nil
}

// A 429 recorded while a request is in flight must survive that request's success.
// The success shows only that the request was answered; the cooldown the server set
// still applies. The second case has an expired state from before the request, so
// the clear has to compare states, not just check whether one existed.
func TestSuccessKeepsCooldownRecordedWhileItsRequestWasInFlight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expired bool
	}{
		{"no earlier state", false},
		{"expired earlier state", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := isolateBackoff(t)
			p := &gatedProvider{
				limitedProvider: limitedProvider{name: "claude", id: "work"},
				started:         make(chan struct{}),
				release:         make(chan struct{}),
			}
			key := backoffKey(p)
			if tc.expired {
				recordRateLimit(key, 0)
				*clock = clock.Add(time.Hour)
			}
			done := make(chan error, 1)
			go func() {
				_, err := FetchSource(context.Background(), p)
				done <- err
			}()
			select {
			case <-p.started:
			case <-time.After(10 * time.Second):
				t.Fatal("the fetch never started")
			}
			want := clock.Add(2 * time.Hour)
			recordRateLimit(key, 2*time.Hour) // another process's 429, while our request is in flight
			close(p.release)
			if err := <-done; err != nil {
				t.Fatalf("FetchSource: %v", err)
			}
			if state, ok := readBackoff(key); !ok || !state.Until.Equal(want) {
				t.Fatalf("after success the stored cooldown ends %v (found %v), want %v", state.Until, ok, want)
			}
		})
	}
}

// The retry time names the day when it falls on a later day, so a wait of several
// days does not read as a time today.
func TestRateLimitedMessageDatesACooldownThatEndsAnotherDay(t *testing.T) {
	clock := isolateBackoff(t)
	*clock = time.Date(2026, 10, 7, 9, 0, 0, 0, time.Local)
	p := &limitedProvider{name: "claude", id: "work"}
	for _, tc := range []struct {
		after time.Duration
		want  string
	}{
		{3 * time.Hour, "rate limited (429), next try 12:00"},
		{72 * time.Hour, "rate limited (429), next try Oct 10 09:00"},
	} {
		if data := rateLimitedData(p, clock.Add(tc.after)); data.Error != tc.want {
			t.Errorf("after %v: Error = %q, want %q", tc.after, data.Error, tc.want)
		}
	}
}

// A later, shorter cooldown must not shorten one still running. A 429 can extend
// the backoff, never end it sooner. A regression guard: it passes on the old code.
func TestLaterShorterCooldownDoesNotShortenAnActiveOne(t *testing.T) {
	clock := isolateBackoff(t)
	key := "claude:work"
	recordRateLimit(key, 2*time.Hour)
	recordRateLimit(key, 0) // the 5-minute floor, which is shorter
	state, ok := readBackoff(key)
	if want := clock.Add(2 * time.Hour); !ok || !state.Until.Equal(want) {
		t.Fatalf("stored cooldown ends %v (found %v), want %v", state.Until, ok, want)
	}
}

// holdBackoffLock takes the backoff lock from outside the code under test, so a test
// can check what a call does while another process holds it.
func holdBackoffLock(t *testing.T) (release func()) {
	t.Helper()
	dir, err := backoffDir()
	if err != nil {
		t.Fatal(err)
	}
	release, err = filelock.Acquire(filepath.Join(dir, ".lock"), time.Second)
	if errors.Is(err, filelock.ErrUnsupported) {
		t.Skip("no file locks on this platform")
	}
	if err != nil {
		t.Fatal(err)
	}
	return release
}

// A read waits for the backoff lock, so it sees what the lock holder wrote before it released
// the lock, unless the wait runs out first. Reads take the lock because Windows can refuse to
// replace a file another handle has open. The holder here stands in for another process: it
// writes a newer cooldown while it holds the lock, then releases it. The read must keep waiting
// while the lock is held, and must return that cooldown after the release. The one-minute wait
// allows normal scheduling delays. The outcome assertion rejects any read that gives up and reads
// unlocked.
func TestReadWaitsForTheBackoffLock(t *testing.T) {
	isolateBackoff(t)
	setBackoffLockWait(t, time.Minute)
	busy := make(chan struct{}, 1)
	oldHook := onBackoffLockBusy
	onBackoffLockBusy = func() {
		select {
		case busy <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { onBackoffLockBusy = oldHook })
	outcomes := make(chan bool, 1)
	oldOutcome := onBackoffLockOutcome
	onBackoffLockOutcome = func(locked bool) {
		select {
		case outcomes <- locked:
		default:
		}
	}
	t.Cleanup(func() { onBackoffLockOutcome = oldOutcome })

	key := "claude:work"
	release := holdBackoffLock(t)
	var releaseOnce sync.Once
	releaseLock := func() { releaseOnce.Do(release) }

	got := make(chan backoffState, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state, _ := readBackoff(key)
		got <- state
	}()
	// Cleanups run in reverse order, so this one runs before the globals set above are
	// restored. The read reads those globals, so it must finish first. Once the lock is
	// released, a read that is waiting takes it and returns, so this wait blocks only when
	// the read itself is stuck. Then go test's timeout reports it.
	t.Cleanup(func() {
		releaseLock()
		<-done
	})
	waitForBusy := func(point string) {
		t.Helper()
		select {
		case <-busy:
		case <-done:
			t.Fatalf("the read returned before %s", point)
		case <-time.After(10 * time.Second):
			t.Fatalf("the read did not reach %s", point)
		}
	}
	waitForBusy("its first wait on the lock")
	newer := backoffState{Until: backoffNow().Add(time.Hour), Failures: 1, ID: 7}
	if !writeBackoff(key, newer) {
		t.Fatal("the lock holder could not write its cooldown")
	}
	// The second report establishes repeated contention. The outcome check after the release
	// establishes successful acquisition.
	waitForBusy("a second attempt while the lock is held")
	select {
	case state := <-got:
		t.Fatalf("the read returned %#v while another holder had the lock", state)
	default:
	}
	releaseLock()
	select {
	case state := <-got:
		if state.ID != newer.ID {
			t.Fatalf("the read returned %#v, want the cooldown the holder wrote", state)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the read did not return after the lock was released")
	}
	// A read that gave up after the write and then read the file unlocked would return the same
	// cooldown. The cooldown check cannot show that it waited, but the outcome of its lock
	// attempt can.
	select {
	case locked := <-outcomes:
		if !locked {
			t.Fatal("the read gave up on the busy backoff lock and read without it")
		}
	default:
		t.Fatal("the read did not attempt the backoff lock")
	}
}

// A success must not end a backoff while another holder has the backoff lock. The
// clear waits for the lock, and when the lock stays busy it leaves the state alone.
// A later clean reading then tries again.
func TestClearLeavesTheBackoffWhenTheLockStaysBusy(t *testing.T) {
	clock := isolateBackoff(t)
	key := "claude:work"
	recordRateLimit(key, 0)
	before, _ := readBackoff(key)
	*clock = clock.Add(time.Hour)
	release := holdBackoffLock(t)
	clearBackoff(key, before)
	release()
	if _, ok := readBackoff(key); !ok {
		t.Fatal("a success ended the backoff while another holder had the lock")
	}
}

// A state written by a build without IDs cannot be matched to the request that saw
// it, so a success leaves it alone. The next 429 replaces it with an ID-bearing state.
func TestSuccessLeavesAStateWithoutAnIDAlone(t *testing.T) {
	clock := isolateBackoff(t)
	key := "claude:work"
	writeBackoff(key, backoffState{Until: clock.Add(time.Hour), Failures: 1})
	before, ok := readBackoff(key)
	if !ok {
		t.Fatal("the stored state without an ID was not read")
	}
	clearBackoff(key, before)
	if _, ok := readBackoff(key); !ok {
		t.Fatal("a success cleared a state that has no ID")
	}
}

// shortBackoffLockWait makes a busy lock fail fast. The tests that use it hold the lock
// for the whole call, so the outcome does not depend on the wait.
func shortBackoffLockWait(t *testing.T) {
	setBackoffLockWait(t, 20*time.Millisecond)
}

// setBackoffLockWait sets how long a call waits for a busy backoff lock, for one test.
func setBackoffLockWait(t *testing.T, wait time.Duration) {
	t.Helper()
	old := backoffLockWait
	backoffLockWait = wait
	t.Cleanup(func() { backoffLockWait = old })
}

// resetUnwritten empties the cooldowns held in memory. They are keyed by source, not by
// directory, so a test must not inherit another test's.
func resetUnwritten() {
	unwritten.Lock()
	defer unwritten.Unlock()
	unwritten.byKey = map[string]backoffState{}
}

// peekUnwritten returns the cooldown held in memory for key, if any.
func peekUnwritten(key string) (backoffState, bool) {
	unwritten.Lock()
	defer unwritten.Unlock()
	state, ok := unwritten.byKey[key]
	return state, ok
}

// While another process holds the backoff lock, a 429 does not reach the file. This
// process still skips the source until the cooldown ends.
func TestBusyLockLeavesTheFileAloneAndStillSkipsTheSource(t *testing.T) {
	clock := isolateBackoff(t)
	shortBackoffLockWait(t)
	p := &limitedProvider{name: "claude", id: "work", err: &RateLimitError{RetryAfter: 72 * time.Hour}}
	path, err := backoffPath(backoffKey(p))
	if err != nil {
		t.Fatal(err)
	}
	release := holdBackoffLock(t)
	fetchExpectingCalls(t, p, 1, "429 while the lock is busy")
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the cooldown reached the file while the lock was busy (stat: %v)", err)
	}
	*clock = clock.Add(time.Hour)
	fetchExpectingCalls(t, p, 1, "an hour into a 72-hour cooldown, lock still busy")
	release()
}

// A cooldown held while the lock was busy reaches the file on the next read that holds
// the lock, and is no longer held in memory after that.
func TestBusyLockCooldownIsWrittenByTheNextLockedRead(t *testing.T) {
	isolateBackoff(t)
	shortBackoffLockWait(t)
	key := "claude:work"
	release := holdBackoffLock(t)
	until := recordRateLimit(key, 72*time.Hour)
	release()
	if state, ok := readBackoff(key); !ok || !state.Until.Equal(until) {
		t.Fatalf("locked read = %#v (found %v), want the cooldown ending %v", state, ok, until)
	}
	if stored, ok := loadBackoff(key); !ok || !stored.Until.Equal(until) {
		t.Fatalf("the file holds %#v (found %v) after the locked read, want the cooldown", stored, ok)
	}
	if _, held := peekUnwritten(key); held {
		t.Fatal("the cooldown is still held in memory after the file holds it")
	}
}

// A success drops a cooldown held in memory only when it has the cooldown's ID. A 429
// that the success did not see stays in force.
func TestBusyLockSuccessDropsOnlyTheCooldownItsRequestSaw(t *testing.T) {
	isolateBackoff(t)
	shortBackoffLockWait(t)
	key := "claude:work"
	release := holdBackoffLock(t)
	recordRateLimit(key, 72*time.Hour)
	held, ok := peekUnwritten(key)
	if !ok {
		t.Fatal("the busy-lock 429 was not held in memory")
	}
	clearBackoff(key, backoffState{ID: held.ID + 1})
	if _, ok := peekUnwritten(key); !ok {
		t.Fatal("a success dropped a cooldown that its request did not see")
	}
	clearBackoff(key, backoffState{ID: held.ID})
	if _, ok := peekUnwritten(key); ok {
		t.Fatal("a success that saw the held cooldown did not drop it")
	}
	release()
}

// Recordings that cannot take the backoff lock must each count. Each 429 doubles the
// cooldown, so a lost failure gives a shorter cooldown than the repeated 429s call for.
// The goroutines are released together, so they usually overlap on the held cooldown.
// Nothing forces an overlap, so this is a concurrent stress test, not proof that one happened.
func TestBusyLockRecordingsCountEveryFailure(t *testing.T) {
	isolateBackoff(t)
	shortBackoffLockWait(t)
	key := "claude:work"
	defer holdBackoffLock(t)()
	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recordRateLimit(key, 0)
		}()
	}
	close(start)
	wg.Wait()
	if state, ok := peekUnwritten(key); !ok || state.Failures != n {
		t.Fatalf("held cooldown counts %d failures (found %v), want %d", state.Failures, ok, n)
	}
}

// Every read of the stored state runs while the caller holds unwritten. The stored state and
// the held cooldowns are then one snapshot: no write from this process lands between the two
// reads, because each write also holds the mutex. The hook runs inside loadBackoff, so a free
// mutex there means that read happened outside the critical section.
func TestStoredStateIsReadUnderTheMutex(t *testing.T) {
	isolateBackoff(t)
	shortBackoffLockWait(t)
	var reads, unguarded int
	oldHook := onBackoffSnapshot
	onBackoffSnapshot = func() {
		reads++
		if unwritten.TryLock() {
			unwritten.Unlock()
			unguarded++
		}
	}
	t.Cleanup(func() { onBackoffSnapshot = oldHook })

	key := "claude:work"
	// With the lock free: a record, a read and a clear, each of which reads the stored state.
	// The record's ID is 1, the first ID isolateBackoff counts from. The clear uses that ID:
	// clearBackoff returns before its read for a zero ID, which would make the count four.
	recordRateLimit(key, 0)
	readBackoff(key)
	clearBackoff(key, backoffState{ID: 1})
	// With the file lock held elsewhere, the record is held in memory and the read merges it
	// with the stored state. The clear cannot take the file lock, so it reads no stored state.
	release := holdBackoffLock(t)
	recordRateLimit(key, 0)
	readBackoff(key)
	clearBackoff(key, backoffState{ID: 1})
	release()
	if reads < 5 {
		t.Fatalf("the stored state was read %d times, want at least 5", reads)
	}
	if unguarded != 0 {
		t.Fatalf("%d of %d stored-state reads ran with the mutex free", unguarded, reads)
	}
}

// A 429 that lands while a request is in flight, and is held because the lock is busy,
// survives that request's success once the lock is free. The success clears only the
// cooldown its request read when it was sent.
func TestHeldCooldownFromAnInFlightRequestSurvivesItsSuccess(t *testing.T) {
	clock := isolateBackoff(t)
	shortBackoffLockWait(t)
	key := "claude:work"
	recordRateLimit(key, 0)
	*clock = clock.Add(time.Hour) // the request is sent once the 5-minute cooldown has ended
	before, _ := readBackoff(key)
	release := holdBackoffLock(t)
	want := recordRateLimit(key, 72*time.Hour) // another request's 429, while ours is in flight
	release()
	clearBackoff(key, before)
	if state, ok := readBackoff(key); !ok || !state.Until.Equal(want) {
		t.Fatalf("after the success the cooldown ends %v (found %v), want %v", state.Until, ok, want)
	}
}
