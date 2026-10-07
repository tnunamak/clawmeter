package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// isolateBackoff gives the test its own backoff dir and a controllable clock.
func isolateBackoff(t *testing.T) *time.Time {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldNow := backoffDir, backoffNow
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	backoffDir = func() (string, error) { return dir, nil }
	backoffNow = func() time.Time { return clock }
	t.Cleanup(func() { backoffDir, backoffNow = oldDir, oldNow })
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
	*clock = clock.Add(2 * time.Minute) // 6 min after first 429, past the floor
	fetchExpectingCalls(t, p, 2, "after floor")
	// Second consecutive 429 doubles the delay to 10 minutes.
	*clock = clock.Add(8 * time.Minute)
	fetchExpectingCalls(t, p, 2, "inside doubled 10m backoff")
	*clock = clock.Add(3 * time.Minute)
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

func TestRateLimitHonorsLongRetryAfterUpToCap(t *testing.T) {
	for _, tc := range []struct {
		retryAfter, want time.Duration
	}{
		{0, backoffFloor},
		{30 * time.Second, backoffFloor},
		{30 * time.Minute, 30 * time.Minute},
		{2 * time.Hour, backoffCap},
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
		"":     0,
		"0":    0,
		"-5":   0,
		"120":  2 * time.Minute,
		" 60":  time.Minute,
		"soon": 0,
		clock.Add(20 * time.Minute).Format(http.TimeFormat): 20 * time.Minute,
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
	if _, ok := backoffUntil(backoffKey(p)); !ok {
		t.Fatal("backoff not recorded")
	}
	*clock = clock.Add(10 * time.Minute)
	if data := fetchExpectingCalls(t, p, 1, "after backoff"); data.Error != "" {
		t.Fatalf("fetch after backoff: %q", data.Error)
	}
	*clock = clock.Add(-10 * time.Minute)
	if _, ok := backoffUntil(backoffKey(p)); ok {
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
		if _, ok := backoffUntil(key); !ok {
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
	if _, ok := backoffUntil(backoffKey(p)); !ok {
		t.Fatal("partial reading with a 429 did not start a backoff")
	}
	fetchExpectingCalls(t, &p.limitedProvider, 1, "after partial 429")
}
