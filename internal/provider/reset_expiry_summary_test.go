package provider

import (
	"strings"
	"testing"
	"time"
)

func TestResetExpirySummaryShowsConsecutiveDeadlines(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	r := &UsageResetCredits{AvailableCount: 3, Credits: []UsageResetCredit{
		{Status: "available", ExpiresAt: time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)},
		{Status: "available", ExpiresAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local)},
		{Status: "available", ExpiresAt: time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)},
	}}
	got := r.Summary(now)
	for _, date := range []string{"Nov 6", "Nov 7", "Nov 8"} {
		if !strings.Contains(got, date) {
			t.Errorf("summary hides deadline %q: %s", date, got)
		}
	}
	if strings.Index(got, "Nov 6") > strings.Index(got, "Nov 7") || strings.Index(got, "Nov 7") > strings.Index(got, "Nov 8") {
		t.Errorf("deadlines are not chronological: %s", got)
	}
}

func TestResetExpirySummaryDistinguishesSameDayTimes(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	r := &UsageResetCredits{AvailableCount: 2, Credits: []UsageResetCredit{
		{Status: "available", ExpiresAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local)},
		{Status: "available", ExpiresAt: time.Date(2026, 11, 6, 17, 0, 0, 0, time.Local)},
	}}
	got := r.Summary(now)
	if !strings.Contains(got, "09:00") || !strings.Contains(got, "17:00") {
		t.Fatalf("same-day deadlines are indistinguishable: %s", got)
	}
}

func TestResetExpiryFormattingRetainsOverflowAndUnknowns(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	r := &UsageResetCredits{AvailableCount: 7}
	for i := 0; i < 6; i++ {
		r.Credits = append(r.Credits, UsageResetCredit{Status: "available", ExpiresAt: now.Add(time.Duration(i+1) * 24 * time.Hour)})
	}
	s := r.ExpirySchedule(now)
	got := s.Summary(now, time.UTC)
	if !strings.Contains(got, "+3 more deadlines") || !strings.Contains(got, "1 expiry unknown") || strings.Contains(got, "Nov 8") {
		t.Fatalf("overflow summary = %q", got)
	}
	details := s.Details(time.UTC)
	if len(details) != 7 || !strings.Contains(details[5], "Nov 8, 2026 at 12:00 UTC") || details[6] != "1 expiry unknown" {
		t.Fatalf("details lost overflow or unknowns: %q", details)
	}
}

func TestResetExpiryFormattingDisambiguatesDSTFold(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s := ResetExpirySchedule{Count: 2, ListedCount: 2, Groups: []ResetExpiryGroup{
		{ExpiresAt: time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), Count: 1},
		{ExpiresAt: time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), Count: 1},
	}}
	for _, text := range []string{s.Summary(now, loc), strings.Join(s.Details(loc), "\n")} {
		if !strings.Contains(text, "-05:00") || !strings.Contains(text, "-06:00") || strings.Count(text, "01:30") != 2 {
			t.Fatalf("repeated hour lost its offset: %q", text)
		}
	}
}

func TestResetExpiryFormattingYearAndConflict(t *testing.T) {
	now := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	r := &UsageResetCredits{AvailableCount: 1, Credits: []UsageResetCredit{
		{Status: "available", ExpiresAt: time.Date(2027, 1, 2, 9, 0, 0, 0, time.UTC)},
		{Status: "available"},
	}}
	s := r.ExpirySchedule(now)
	if s.Summary(now, time.UTC) != "inventory differs" {
		t.Fatal("conflicting count advertised as a usable schedule")
	}
	details := strings.Join(s.Details(time.UTC), "\n")
	for _, want := range []string{"Provider reports 1; 2 unconsumed entries listed", "Jan 2, 2027", "1 expiry unknown in listed entries"} {
		if !strings.Contains(details, want) {
			t.Fatalf("missing %q: %s", want, details)
		}
	}
	r.AvailableCount = 2
	if got := r.ExpirySchedule(now).Summary(now, time.UTC); !strings.Contains(got, "Jan 2, 2027") {
		t.Fatal(got)
	}
}
