package config

import (
	"testing"
	"time"
)

func TestPollIntervalEnforcesFiveMinuteFloor(t *testing.T) {
	tests := map[int]time.Duration{
		0:   5 * time.Minute,
		60:  5 * time.Minute,
		299: 5 * time.Minute,
		300: 5 * time.Minute,
		600: 10 * time.Minute,
	}
	for configured, want := range tests {
		if got := PollInterval(configured); got != want {
			t.Fatalf("PollInterval(%d) = %s, want %s", configured, got, want)
		}
	}
}
