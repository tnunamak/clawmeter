package resetfeed

import (
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// Visible returns the announcement that bears on one Codex reading at now, or
// nil. It is pure apart from forecast.Project, which reads the wall clock.
//
// A window bears on the announcement when it is out now, or is projected to
// run out before its own reset, and its own reset comes after the announced
// time. The announcement shows only while the reading is fresh, the scheduled
// time is at most six hours in the past, and the feed was read successfully
// within two hours.
func Visible(data *provider.UsageData, r Reading, now time.Time) *provider.ResetAnnouncement {
	if data == nil || data.Provider != "openai" {
		return nil
	}
	a := r.Announcement
	if a == nil || r.CheckedAt.IsZero() {
		return nil
	}
	if data.Error != "" || data.Stale || data.IsExpired {
		return nil
	}
	if now.Sub(r.CheckedAt) > LastGoodLimit {
		return nil
	}
	if now.Sub(a.ScheduledFor) > PastLimit {
		return nil
	}

	var windows []string
	for _, window := range data.PresentationWindows() {
		if bearsOn(window, a.ScheduledFor, now) {
			windows = append(windows, window.Name)
		}
	}
	if len(windows) == 0 {
		return nil
	}
	return &provider.ResetAnnouncement{
		Kind:          provider.ResetAnnouncementKind,
		Official:      false,
		Feed:          provider.ResetAnnouncementFeed,
		SourceURL:     a.SourceURL,
		AnnouncedAt:   a.AnnouncedAt,
		ScheduledFor:  a.ScheduledFor,
		Approximate:   true,
		Windows:       windows,
		FeedCheckedAt: r.CheckedAt,
		ShownUntil:    a.ScheduledFor.Add(PastLimit),
	}
}

// bearsOn reports whether a window is blocked at the announced time: it is out
// now, or the current rate runs it out before its own reset, and that reset
// is later than the announcement. A window whose reset has passed carries a
// stale reading and does not qualify.
func bearsOn(window provider.UsageWindow, scheduledFor, now time.Time) bool {
	if window.ResetsAt.IsZero() || !window.ResetsAt.After(now) || !window.ResetsAt.After(scheduledFor) {
		return false
	}
	if window.Utilization >= 100 {
		return true
	}
	proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.WindowLength(window))
	if proj.Unknown || proj.Stale {
		return false
	}
	return !proj.WillLastToReset
}

// Announce returns data with its AnnouncedReset set to the announcement
// visible at now. It returns a copy and never changes the input, so quota
// values, projections, and reset times are untouched. Data without a visible
// announcement comes back unchanged.
func Announce(data *provider.UsageData, r Reading, now time.Time) *provider.UsageData {
	if data == nil {
		return nil
	}
	visible := Visible(data, r, now)
	if visible == nil && data.AnnouncedReset == nil {
		return data
	}
	copied := data.Clone()
	copied.AnnouncedReset = visible
	return copied
}

// AnnounceAll applies Announce to every source. It returns a new map.
func AnnounceAll(results map[string]*provider.UsageData, r Reading, now time.Time) map[string]*provider.UsageData {
	if results == nil {
		return nil
	}
	out := make(map[string]*provider.UsageData, len(results))
	for key, data := range results {
		out[key] = Announce(data, r, now)
	}
	return out
}
