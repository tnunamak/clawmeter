//go:build tray

package tray

import "time"

// loginCheckInterval is how often the tray looks for a changed login, such as
// Claude Code renewing its token. Each check only reads file metadata.
const loginCheckInterval = 20 * time.Second

// loginRefreshGap is the least time between two refreshes that a login change
// starts, so a tool that rewrites its login file often cannot make the tray
// poll providers more than once a minute.
const loginRefreshGap = time.Minute

// loginWatch decides when a changed login needs a refresh. A reading belongs
// to the login it was read with, so after a change the cache serves nothing
// for that source until the next refresh; without this the gap lasts up to a
// whole poll interval.
type loginWatch struct {
	seen    map[string]string
	pending bool
	lastRun time.Time
}

// due records the current revisions and reports whether a refresh should
// start now. The first check only records them. A change stays pending until
// ran is called, so a refresh that could not start is tried again.
func (w *loginWatch) due(current map[string]string, now time.Time) bool {
	if w.seen != nil {
		for name, revision := range current {
			if previous, ok := w.seen[name]; !ok || previous != revision {
				w.pending = true
			}
		}
	}
	w.seen = current
	return w.pending && now.Sub(w.lastRun) >= loginRefreshGap
}

// ran records that a refresh started at now, after every change seen so far.
func (w *loginWatch) ran(now time.Time) {
	w.pending = false
	w.lastRun = now
}
