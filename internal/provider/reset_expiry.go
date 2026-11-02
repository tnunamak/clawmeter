package provider

import (
	"fmt"
	"strings"
	"time"
)

// ResetExpiryGroup counts observed grants sharing one absolute expiry instant.
type ResetExpiryGroup struct {
	ExpiresAt time.Time `json:"expires_at"`
	Count     int       `json:"count"`
}

// ResetExpirySchedule preserves count authority separately from listed observations.
// It does not establish inventory completeness or entitlement.
type ResetExpirySchedule struct {
	Count            int                `json:"count"`
	Groups           []ResetExpiryGroup `json:"groups"`
	UnknownCount     int                `json:"unknown_count"`
	ListedCount      int                `json:"listed_count"`
	ReportedCount    int                `json:"reported_count"`
	ConflictingCount bool               `json:"conflicting_count"`
}

// ExpirySchedule evaluates the existing inventory semantics at one supplied time.
// Equal timestamps combine counts, not identities; source data is never mutated.
func (r *UsageResetCredits) ExpirySchedule(now time.Time) ResetExpirySchedule {
	out := ResetExpirySchedule{Count: r.DisplayCount(now), Groups: make([]ResetExpiryGroup, 0)}
	if r == nil {
		return out
	}
	out.ReportedCount = r.AvailableCount
	if out.ReportedCount < 0 {
		out.ReportedCount = 0
	}
	available := r.Available(now)
	out.ListedCount = len(available)
	out.ConflictingCount = !r.Snapshot && out.ReportedCount > 0 && out.ReportedCount < out.ListedCount
	known := 0
	for _, credit := range available {
		if credit.ExpiresAt.IsZero() {
			continue
		}
		known++
		n := len(out.Groups)
		if n > 0 && out.Groups[n-1].ExpiresAt.Equal(credit.ExpiresAt) {
			out.Groups[n-1].Count++
		} else {
			out.Groups = append(out.Groups, ResetExpiryGroup{ExpiresAt: credit.ExpiresAt, Count: 1})
		}
	}
	// Aggregate earliest identifies at most one grant, and never supplements a
	// partial eligible list because it may describe a grant already in that list.
	if out.ListedCount == 0 && out.Count > 0 && r.EarliestExpiresAt.After(now) && !r.EarliestExpiresAt.IsZero() {
		out.Groups = append(out.Groups, ResetExpiryGroup{ExpiresAt: r.EarliestExpiresAt, Count: 1})
		known = 1
	}
	out.UnknownCount = out.Count - known
	if out.ConflictingCount {
		out.UnknownCount = out.ListedCount - known
	}
	if out.UnknownCount < 0 {
		out.UnknownCount = 0
	}
	return out
}

// Summary lists common deadlines without making long inventories widen a menu.
// Exact deadlines remain available through Details and the typed Groups field.
func (s ResetExpirySchedule) Summary(now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	if s.ConflictingCount {
		return "inventory differs"
	}
	if len(s.Groups) == 0 {
		return "expiry dates unknown"
	}
	dayCounts := make(map[string]int)
	wallCounts := make(map[string]int)
	for _, g := range s.Groups {
		t := g.ExpiresAt.In(loc)
		dayCounts[t.Format("2006-01-02")]++
		wallCounts[t.Format("2006-01-02 15:04")]++
	}
	limit := len(s.Groups)
	if limit > 4 {
		limit = 3
	}
	var dates []string
	for _, g := range s.Groups[:limit] {
		t := g.ExpiresAt.In(loc)
		date := t.Format("Jan 2")
		if t.Year() != now.In(loc).Year() {
			date = t.Format("Jan 2, 2006")
		}
		if t.Format("2006-01-02") == now.In(loc).Format("2006-01-02") {
			date = expiryClock(t)
		} else if dayCounts[t.Format("2006-01-02")] > 1 {
			date += " " + expiryClock(t)
		}
		if wallCounts[t.Format("2006-01-02 15:04")] > 1 {
			date += " " + t.Format("-07:00")
		}
		if g.Count > 1 {
			date += fmt.Sprintf(" ×%d", g.Count)
		}
		dates = append(dates, date)
	}
	verb := "expire "
	if s.Count == 1 {
		verb = "expires "
	}
	if s.ListedCount == 0 && s.UnknownCount > 0 {
		verb = "next expires "
	}
	parts := []string{verb + strings.Join(dates, ", ")}
	if len(s.Groups) > limit {
		parts = append(parts, fmt.Sprintf("+%d more deadlines", len(s.Groups)-limit))
	}
	if s.UnknownCount > 0 {
		parts = append(parts, expiryUnknown(s.UnknownCount))
	}
	return strings.Join(parts, " · ")
}

// Details keeps every deadline, including local zone offsets during DST folds.
func (s ResetExpirySchedule) Details(loc *time.Location) []string {
	if loc == nil {
		loc = time.Local
	}
	var lines []string
	if s.ConflictingCount {
		lines = append(lines, fmt.Sprintf("Provider reports %d; %d unconsumed entries listed", s.ReportedCount, s.ListedCount))
	}
	for _, g := range s.Groups {
		t := g.ExpiresAt.In(loc)
		noun := "reset"
		if g.Count != 1 {
			noun = "resets"
		}
		label := fmt.Sprintf("%d %s", g.Count, noun)
		if s.ConflictingCount {
			label += " listed"
		}
		lines = append(lines, label+" · "+t.Format("Jan 2, 2006")+" at "+expiryClock(t)+" "+t.Format("MST (UTC-07:00)"))
	}
	if s.UnknownCount > 0 {
		label := expiryUnknown(s.UnknownCount)
		if s.ConflictingCount {
			label += " in listed entries"
		}
		lines = append(lines, label)
	}
	return lines
}

func expiryUnknown(count int) string {
	if count == 1 {
		return "1 expiry unknown"
	}
	return fmt.Sprintf("%d expiries unknown", count)
}

func expiryClock(t time.Time) string {
	if t.Second() != 0 || t.Nanosecond() != 0 {
		return t.Format("15:04:05.999999999")
	}
	return t.Format("15:04")
}
