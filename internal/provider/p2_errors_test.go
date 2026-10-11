package provider

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
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

func TestSafeFetchErrorTypedNetworkWithPortAndTokenURL(t *testing.T) {
	for _, port := range []int{40123, 40321, 42999} {
		err := &url.Error{Op: "Post", URL: fmt.Sprintf("http://localhost:%d/token", port), Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("host unreachable")}}
		if got := SafeFetchError(err); got != "connection failed" {
			t.Errorf("typed network port %d: got %q, want connection failed", port, got)
		}
	}
}
