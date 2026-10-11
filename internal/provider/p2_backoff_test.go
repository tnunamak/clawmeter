package provider

import (
	"testing"
	"time"
)

func TestP2BackoffNeverShortensExistingDeadline(t *testing.T) {
	isolateBackoff(t)
	first := recordRateLimit("synthetic-source", 30*time.Minute)
	second := recordRateLimit("synthetic-source", 5*time.Minute)
	if second.Before(first) {
		t.Fatalf("active deadline shortened from %v to %v", first, second)
	}
}
