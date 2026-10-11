package provider

import (
	"errors"
	"testing"
)

func TestP2ExplicitHTTPStatus(t *testing.T) {
	for input, want := range map[string]string{"HTTP status 429": "rate limited", "status code: 401": "authentication failed", "HTTP 42999 connection refused": "connection failed"} {
		if got := SafeFetchError(errors.New(input)); got != want {
			t.Errorf("%q: got %q want %q", input, got, want)
		}
	}
}
