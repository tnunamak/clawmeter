package antigravity

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
)

func TestQuotaSummaryDiscoversNewPoolsAndCadences(t *testing.T) {
	body := []byte(`{"groups":[{"displayName":"Future reasoning models","description":"Models within this group: New Model 12","buckets":[{"bucketId":"reasoning-session","displayName":"Session remaining","window":"5h","remainingFraction":0,"resetTime":"2026-07-29T16:00:00Z"},{"bucketId":"reasoning-daily","window":"daily","remainingFraction":0.4,"resetTime":"2026-07-30T12:00:00Z"}]}]}`)
	windows, err := parseQuotaSummary(body, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 || windows[0].Name != "reasoning-session" || windows[0].Length != 5*time.Hour || windows[1].Length != 24*time.Hour {
		t.Fatalf("windows=%+v", windows)
	}
	if windows[0].Scope != "Models within this group: New Model 12" || windows[0].DisplayName != "5 hours (Future reasoning)" {
		t.Fatalf("lost upstream scope: %+v", windows[0])
	}
}

func TestQuotaSummaryUnknownCadenceDoesNotInheritWeeklyFromID(t *testing.T) {
	windows, err := parseQuotaSummary([]byte(`{"groups":[{"displayName":"Renamed upstream pool","buckets":[{"bucketId":"3p-weekly","window":"adaptive","remainingFraction":0.8,"resetTime":"2026-07-30T12:00:00Z"}]}]}`), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if forecast.WindowLength(windows[0]) != 0 || windows[0].DisplayName != "Renamed upstream pool" {
		t.Fatalf("invented cadence or label: %+v", windows[0])
	}
}

func TestQuotaSummaryMissingPoolAndResetRemainUnknown(t *testing.T) {
	windows, err := parseQuotaSummary([]byte(`{"groups":[{"displayName":"Partner models","buckets":[{"bucketId":"3p-weekly","window":"weekly","remainingFraction":1}]}]}`), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || !windows[0].ResetsAt.IsZero() {
		t.Fatalf("fabricated missing quota/reset: %+v", windows)
	}
}

func TestQuotaSummaryPreservesSameCadenceBucketMeaning(t *testing.T) {
	windows, err := parseQuotaSummary([]byte(`{"groups":[{"displayName":"Future Models","buckets":[{"bucketId":"a","displayName":"Text","description":"Text allowance","window":"daily","remainingFraction":0.7},{"bucketId":"b","displayName":"Images","description":"Image allowance","window":"daily","remainingFraction":0.5}]}]}`), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if windows[0].DisplayName == windows[1].DisplayName || windows[0].Description != "Text allowance" || windows[1].Description != "Image allowance" {
		t.Fatalf("lost bucket meaning: %+v", windows)
	}
}
