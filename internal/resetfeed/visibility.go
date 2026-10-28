package resetfeed

import (
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

// Visible returns the announcement that bears on one Codex reading at now, or
// nil. It uses the same evaluated window and age allowance as the display.
//
// A window bears on the announcement when it is out now, or is projected to
// run out before its own reset, and its own reset comes after the announced
// time. The announcement shows only while the reading is usable, the scheduled
// time is at most six hours in the past, and the feed was read successfully
// within two hours.
func Visible(data *provider.UsageData, r Reading, now time.Time, maxReadingAge time.Duration) *provider.ResetAnnouncement {
	if data == nil || data.Provider != "openai" {
		return nil
	}
	a := r.Announcement
	if a == nil || r.CheckedAt.IsZero() {
		return nil
	}
	if now.Sub(r.CheckedAt) > LastGoodLimit {
		return nil
	}
	if now.Sub(a.ScheduledFor) > PastLimit {
		return nil
	}

	var windows []string
	snap := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"source": data}, MaxReadingAge: maxReadingAge}, now)
	for _, window := range snap.Accounts[0].Windows {
		if bearsOn(window, a.ScheduledFor, data.FetchedAt, now) {
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

// bearsOn reports whether the announcement bears on a window. Before the
// announced time, the window must be out now or run out before its own reset
// at the observed average rate. Once the time has passed, only a window that is still
// out counts, in a reading taken after that time: an announced reset that has
// passed changes nothing for a window with room, and a reading from before it
// cannot say whether the window is still out. Either way the window's own
// reset must come after the announced time, and a window whose reset has
// passed carries a stale reading and does not count.
func bearsOn(window view.Window, scheduledFor, fetchedAt, now time.Time) bool {
	if !window.Reset.Known || !window.Reset.At.After(now) || !window.Reset.At.After(scheduledFor) {
		return false
	}
	if !scheduledFor.After(now) {
		return window.Exhaustion.State == view.ExhaustionOut && fetchedAt.After(scheduledFor)
	}
	return window.Exhaustion.State == view.ExhaustionOut || window.Exhaustion.State == view.ExhaustionRunsOut
}

// Announce returns data with its AnnouncedReset set to the announcement
// visible at now. It returns a copy and never changes the input, so quota
// values, projections, and reset times are untouched. Data without a visible
// announcement comes back unchanged.
func Announce(data *provider.UsageData, r Reading, now time.Time, maxReadingAge time.Duration) *provider.UsageData {
	if data == nil {
		return nil
	}
	visible := Visible(data, r, now, maxReadingAge)
	if visible == nil && data.AnnouncedReset == nil {
		return data
	}
	copied := data.Clone()
	copied.AnnouncedReset = visible
	return copied
}

// AnnounceAll applies Announce to every source. It returns a new map.
func AnnounceAll(results map[string]*provider.UsageData, r Reading, now time.Time, maxReadingAge time.Duration) map[string]*provider.UsageData {
	if results == nil {
		return nil
	}
	out := make(map[string]*provider.UsageData, len(results))
	for key, data := range results {
		out[key] = Announce(data, r, now, maxReadingAge)
	}
	return out
}
