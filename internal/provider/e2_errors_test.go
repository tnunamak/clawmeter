package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"
)

type e2HTTPError int

func (e e2HTTPError) Error() string       { return "upstream rejected request" }
func (e e2HTTPError) HTTPStatusCode() int { return int(e) }

func TestSafeFetchErrorPortsAndTypedStatus(t *testing.T) {
	for _, port := range []int{40123, 40321, 42999} {
		err := fmt.Errorf("network read failed at 127.0.0.1:%d", port)
		if got := SafeFetchError(err); got != "connection failed" {
			t.Errorf("port %d: got %q, want connection failed", port, got)
		}
	}
	for _, code := range []int{401, 403, 429} {
		want := "authentication failed"
		if code == 429 {
			want = "rate limited"
		}
		if got := SafeFetchError(fmt.Errorf("wrapped: %w", e2HTTPError(code))); got != want {
			t.Errorf("typed HTTP %d: got %q, want %q", code, got, want)
		}
	}
	if got := SafeFetchError(fmt.Errorf("wrapped: %w", &RateLimitError{})); got != "rate limited" {
		t.Errorf("typed rate limit: %q", got)
	}
}

func TestFormatMinorUnitsSigned(t *testing.T) {
	for minor, want := range map[int]string{-50: "$-0.50", -150: "$-1.50", 0: "$0.00", 150: "$1.50"} {
		if got := formatMinorUnits(minor, "USD"); got != want {
			t.Errorf("minor %d: got %q, want %q", minor, got, want)
		}
	}
}

type e2BlockingProvider struct {
	fakeSourceProvider
	started, release, finished chan struct{}
}

func (p e2BlockingProvider) FetchUsage(context.Context) (*UsageData, error) {
	close(p.started)
	<-p.release
	defer close(p.finished)
	return &UsageData{Provider: p.Name()}, nil
}
func TestFetchProvidersParallelCancelsIgnoringWorker(t *testing.T) {
	p := e2BlockingProvider{fakeSourceProvider: fakeSourceProvider{id: "blocked"}, started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan *MultiFetchResult, 1)
	go func() { returned <- FetchProvidersParallel(ctx, []Provider{fakeSourceProvider{id: "ready"}, p}) }()
	<-p.started
	// Allow the fast worker to report before cancellation.
	time.Sleep(20 * time.Millisecond)
	cancel()
	var result *MultiFetchResult
	select {
	case result = <-returned:
	case <-time.After(time.Second):
		close(p.release)
		<-returned
		t.Fatal("fetch did not return after cancellation while provider ignored context")
	}
	if result.Results["fake:ready"] == nil {
		t.Error("completed result was lost")
	}
	if !errors.Is(result.Errors["fake:blocked"], context.Canceled) {
		t.Errorf("pending error = %v, want canceled", result.Errors)
	}
	close(p.release)
	<-p.finished
	// A late worker must never mutate returned maps (also checked under -race).
	for i := 0; i < 1000; i++ {
		if result.Results["fake:blocked"] == nil || result.Results["fake:blocked"].Error != "connection timed out" {
			t.Fatalf("pending data changed: %#v", result.Results)
		}
	}
}

func TestSafeFetchErrorTypedNetworkWithPortAndTokenURL(t *testing.T) {
	for _, port := range []int{40123, 40321, 42999} {
		err := &url.Error{Op: "Post", URL: fmt.Sprintf("http://localhost:%d/token", port), Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("host unreachable")}}
		if got := SafeFetchError(err); got != "connection failed" {
			t.Errorf("typed network port %d: got %q, want connection failed", port, got)
		}
	}
}

func TestE2RedteamTypedServerStatusPrecedesWrapperKeywords(t *testing.T) {
	for _, prefix := range []string{
		"refresh token endpoint",
		"rate limit service unavailable",
		"upstream connection pool exhausted",
	} {
		t.Run(prefix, func(t *testing.T) {
			err := fmt.Errorf("%s: %w", prefix, &HTTPError{StatusCode: 503})
			if got := ClassifyError(err); got != "api" {
				t.Errorf("typed HTTP 503 classified as %q, want api", got)
			}
			if got := SafeFetchError(err); got != "provider request failed" {
				t.Errorf("typed HTTP 503 rendered as %q, want provider request failed", got)
			}
		})
	}
}
