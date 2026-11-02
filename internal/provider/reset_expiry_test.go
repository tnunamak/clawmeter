package provider

import (
	"reflect"
	"testing"
	"time"
)

func TestResetExpiryScheduleCases(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	a, b := now.Add(time.Hour), now.Add(2*time.Hour)
	credit := func(at time.Time) UsageResetCredit { return UsageResetCredit{Status: "available", ExpiresAt: at} }
	group := func(at time.Time, n int) ResetExpiryGroup { return ResetExpiryGroup{ExpiresAt: at, Count: n} }
	for _, tc := range []struct {
		name                             string
		in                               *UsageResetCredits
		count, listed, reported, unknown int
		conflict                         bool
		groups                           []ResetExpiryGroup
	}{
		{name: "nil"},
		{name: "empty", in: &UsageResetCredits{}},
		{name: "sorted duplicates", in: &UsageResetCredits{Credits: []UsageResetCredit{credit(b), credit(a), credit(a)}}, count: 3, listed: 3, groups: []ResetExpiryGroup{group(a, 2), group(b, 1)}},
		{name: "equal instant different offset", in: &UsageResetCredits{Credits: []UsageResetCredit{credit(a), credit(a.In(time.FixedZone("other", -6*3600)))}}, count: 2, listed: 2, groups: []ResetExpiryGroup{group(a, 2)}},
		{name: "unknown expiry", in: &UsageResetCredits{Credits: []UsageResetCredit{credit(time.Time{}), credit(a)}}, count: 2, listed: 2, unknown: 1, groups: []ResetExpiryGroup{group(a, 1)}},
		{name: "aggregate larger", in: &UsageResetCredits{AvailableCount: 5, Credits: []UsageResetCredit{credit(a), credit(time.Time{})}}, count: 5, listed: 2, reported: 5, unknown: 4, groups: []ResetExpiryGroup{group(a, 1)}},
		{name: "aggregate smaller", in: &UsageResetCredits{AvailableCount: 1, Credits: []UsageResetCredit{credit(a), credit(b), credit(time.Time{})}}, count: 1, listed: 3, reported: 1, unknown: 1, conflict: true, groups: []ResetExpiryGroup{group(a, 1), group(b, 1)}},
		{name: "count only", in: &UsageResetCredits{AvailableCount: 4}, count: 4, reported: 4, unknown: 4},
		{name: "aggregate earliest", in: &UsageResetCredits{AvailableCount: 4, EarliestExpiresAt: a}, count: 4, reported: 4, unknown: 3, groups: []ResetExpiryGroup{group(a, 1)}},
		{name: "no fallback with partial list", in: &UsageResetCredits{AvailableCount: 3, EarliestExpiresAt: a, Credits: []UsageResetCredit{credit(b)}}, count: 3, listed: 1, reported: 3, unknown: 2, groups: []ResetExpiryGroup{group(b, 1)}},
		{name: "no fallback with unknown eligible list", in: &UsageResetCredits{AvailableCount: 3, EarliestExpiresAt: a, Credits: []UsageResetCredit{credit(time.Time{})}}, count: 3, listed: 1, reported: 3, unknown: 3},
		{name: "fallback after all listed ineligible", in: &UsageResetCredits{AvailableCount: 3, EarliestExpiresAt: a, Credits: []UsageResetCredit{credit(now)}}, count: 3, reported: 3, unknown: 2, groups: []ResetExpiryGroup{group(a, 1)}},
		{name: "elapsed aggregate earliest", in: &UsageResetCredits{AvailableCount: 3, EarliestExpiresAt: now}, reported: 3},
		{name: "negative aggregate derives list", in: &UsageResetCredits{AvailableCount: -2, Credits: []UsageResetCredit{credit(a)}}, count: 1, listed: 1, groups: []ResetExpiryGroup{group(a, 1)}},
		{name: "negative count only", in: &UsageResetCredits{AvailableCount: -2}},
		{name: "filter status consumed expiry", in: &UsageResetCredits{Credits: []UsageResetCredit{{Status: "consumed", ExpiresAt: a}, {Status: "available", ExpiresAt: a, ConsumedAt: now}, credit(now.Add(-time.Nanosecond)), credit(now), {Status: " AVAILABLE ", ExpiresAt: now.Add(time.Nanosecond)}, {ExpiresAt: b}}}, count: 1, listed: 1, groups: []ResetExpiryGroup{group(now.Add(time.Nanosecond), 1)}},
		{name: "snapshot ages without conflict", in: &UsageResetCredits{Snapshot: true, AvailableCount: 3, Credits: []UsageResetCredit{credit(now), credit(a), credit(time.Time{})}}, count: 2, listed: 2, reported: 3, unknown: 1, groups: []ResetExpiryGroup{group(a, 1)}},
		{name: "snapshot aggregate smaller not conflict", in: &UsageResetCredits{Snapshot: true, AvailableCount: 1, Credits: []UsageResetCredit{credit(a), credit(b)}}, count: 2, listed: 2, reported: 1, groups: []ResetExpiryGroup{group(a, 1), group(b, 1)}},
		{name: "snapshot all expired", in: &UsageResetCredits{Snapshot: true, AvailableCount: 3, EarliestExpiresAt: b, Credits: []UsageResetCredit{credit(now)}}, reported: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before *UsageResetCredits
			if tc.in != nil {
				copy := *tc.in
				if tc.in.Credits != nil {
					copy.Credits = append([]UsageResetCredit{}, tc.in.Credits...)
				}
				before = &copy
			}
			got := tc.in.ExpirySchedule(now)
			if got.Count != tc.count || got.ListedCount != tc.listed || got.ReportedCount != tc.reported || got.UnknownCount != tc.unknown || got.ConflictingCount != tc.conflict {
				t.Fatalf("schedule = %+v", got)
			}
			if len(got.Groups) != len(tc.groups) {
				t.Fatalf("groups = %+v, want %+v", got.Groups, tc.groups)
			}
			for i, want := range tc.groups {
				if got.Groups[i].Count != want.Count || !got.Groups[i].ExpiresAt.Equal(want.ExpiresAt) {
					t.Fatalf("group %d = %+v, want %+v", i, got.Groups[i], want)
				}
			}
			if !reflect.DeepEqual(tc.in, before) {
				t.Fatal("input mutated")
			}
			if got.Groups == nil {
				t.Fatal("groups must encode as an empty list, not null")
			}
		})
	}
}

func TestResetExpiryScheduleAgesAtBoundary(t *testing.T) {
	at := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	r := &UsageResetCredits{Snapshot: true, AvailableCount: 2, Credits: []UsageResetCredit{{Status: "available", ExpiresAt: at}, {Status: "available", ExpiresAt: at.Add(time.Hour)}}}
	if got := r.ExpirySchedule(at.Add(-time.Nanosecond)); got.Count != 2 || len(got.Groups) != 2 {
		t.Fatalf("before expiry: %+v", got)
	}
	if got := r.ExpirySchedule(at); got.Count != 1 || len(got.Groups) != 1 || got.ConflictingCount {
		t.Fatalf("at expiry: %+v", got)
	}
	if got := r.ExpirySchedule(at.Add(time.Hour)); got.Count != 0 || len(got.Groups) != 0 || got.UnknownCount != 0 {
		t.Fatalf("all expired: %+v", got)
	}
}
