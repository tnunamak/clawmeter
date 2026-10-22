//go:build tray

package tray

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	// exhaustionFile keeps the record across tray restarts.
	exhaustionFile = "exhaustion.json"
	// exhaustionResetTolerance absorbs the jitter providers show when they
	// derive resets_at from the time remaining.
	exhaustionResetTolerance = 2 * time.Minute
	// exhaustionRetention keeps a finished reset period in the file for a day.
	exhaustionRetention = 24 * time.Hour
)

// exhaustionRecord is what the tray saw of one window in one reset period.
type exhaustionRecord struct {
	// ResetsAt is the first reset seen for the period. Later readings are
	// compared with it, so jitter cannot drift the period along.
	ResetsAt time.Time `json:"resets_at"`
	// LastBelow is the latest reading under 100%.
	LastBelow time.Time `json:"last_below"`
	// FirstFull is the first reading at 100% since LastBelow.
	FirstFull time.Time `json:"first_full"`
}

// exhaustionLog says when a quota window ran out, but only when the tray saw
// the change: a reading under 100% followed by one at 100%, with the two
// readings close enough together. A window first seen full, or with readings
// far apart (the machine slept, or the tray was off), has no time.
type exhaustionLog struct {
	Windows map[string]exhaustionRecord `json:"windows"`
}

func exhaustionKey(source, window string) string {
	return source + "\x00" + window
}

func sameReset(a, b time.Time) bool {
	gap := a.Sub(b)
	if gap < 0 {
		gap = -gap
	}
	return gap <= exhaustionResetTolerance
}

// observe records one reading of a window taken at the given time. A window
// without a reset is not tracked.
func (l *exhaustionLog) observe(source string, w provider.UsageWindow, at time.Time) {
	if w.ResetsAt.IsZero() {
		return
	}
	if l.Windows == nil {
		l.Windows = make(map[string]exhaustionRecord)
	}
	key := exhaustionKey(source, w.Name)
	rec, ok := l.Windows[key]
	if !ok || !sameReset(rec.ResetsAt, w.ResetsAt) {
		rec = exhaustionRecord{ResetsAt: w.ResetsAt}
	}
	if w.Utilization < 100 {
		rec.LastBelow = at
		rec.FirstFull = time.Time{}
	} else if rec.FirstFull.IsZero() {
		rec.FirstFull = at
	}
	l.Windows[key] = rec
}

// ranOutAt returns when the window ran out, if the tray saw it happen within
// maxGap. The window must be full now, in the same reset period as the
// record.
func (l *exhaustionLog) ranOutAt(source string, w provider.UsageWindow, maxGap time.Duration) (time.Time, bool) {
	rec, ok := l.Windows[exhaustionKey(source, w.Name)]
	if !ok || w.ResetsAt.IsZero() || w.Utilization < 100 || !sameReset(rec.ResetsAt, w.ResetsAt) {
		return time.Time{}, false
	}
	if rec.LastBelow.IsZero() || rec.FirstFull.IsZero() || rec.FirstFull.Sub(rec.LastBelow) > maxGap {
		return time.Time{}, false
	}
	return rec.FirstFull, true
}

// prune drops periods that ended more than exhaustionRetention ago.
func (l *exhaustionLog) prune(now time.Time) {
	for key, rec := range l.Windows {
		if now.Sub(rec.ResetsAt) > exhaustionRetention {
			delete(l.Windows, key)
		}
	}
}

// loadExhaustionLog reads the saved log. A missing or unreadable file starts
// an empty log, so the tray never fails to start over this record.
func loadExhaustionLog(path string) exhaustionLog {
	var l exhaustionLog
	if path == "" {
		return l
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return l
	}
	if err := json.Unmarshal(data, &l); err != nil {
		log.Printf("ignore quota exhaustion log %s: %v", path, err)
		return exhaustionLog{}
	}
	return l
}

// save prunes the log and writes it atomically. An empty path keeps the log
// in memory only.
func (l *exhaustionLog) save(path string, now time.Time) error {
	if path == "" {
		return nil
	}
	l.prune(now)
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".exhaustion-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// windowExhaustion is the tray's log. Refresh observes into it, and the menu
// and tooltip read from it, on different goroutines.
var windowExhaustion exhaustionTracker

// exhaustionTracker guards an exhaustionLog and the settings it needs. Its
// zero value is an empty log that is not saved.
type exhaustionTracker struct {
	mu     sync.Mutex
	path   string
	maxGap time.Duration
	log    exhaustionLog
}

// load replaces the log with the one saved at path, and sets maxGap, the
// longest gap between two readings that still counts as a transition.
func (t *exhaustionTracker) load(path string, maxGap time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.path = path
	t.maxGap = maxGap
	t.log = loadExhaustionLog(path)
}

// observeFetched records the windows of the results this refresh fetched.
// Cached fallbacks are skipped: they are not readings taken now, and a
// reading's own FetchedAt is used when set.
func (t *exhaustionTracker) observeFetched(results, cached map[string]*provider.UsageData, now time.Time, maxGap time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxGap = maxGap
	for name, data := range results {
		if _, wasCached := cached[name]; wasCached || !freshReading(data) {
			continue
		}
		at := data.FetchedAt
		if at.IsZero() {
			at = now
		}
		for _, w := range data.Windows {
			t.log.observe(name, w, at)
		}
	}
	if err := t.log.save(t.path, now); err != nil {
		log.Printf("save quota exhaustion log: %v", err)
	}
}

// ranOutAt returns when the window ran out, or zero when the tray cannot say.
func (t *exhaustionTracker) ranOutAt(source string, w provider.UsageWindow) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.log.ranOutAt(source, w, t.maxGap)
	if !ok {
		return time.Time{}
	}
	return at
}

// freshReading reports whether data came from a successful fetch. Stale,
// errored, and expired data are not readings of the account's state now.
func freshReading(data *provider.UsageData) bool {
	return data != nil && data.Error == "" && !data.Stale && !data.IsExpired
}

// exhaustionPath is where the log is saved, or empty when there is no cache
// directory.
func exhaustionPath() string {
	path, err := cache.Path(exhaustionFile)
	if err != nil {
		log.Printf("quota exhaustion log is not saved: %v", err)
		return ""
	}
	return path
}

// runOutSince writes when a window ran out: a time of day when it was today,
// otherwise a date and time.
func runOutSince(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	clock := at.Format("15:04")
	if at.Year() == now.Year() && at.YearDay() == now.YearDay() {
		return clock
	}
	return at.Format("Jan 2") + " " + clock
}

// ranOutLabel is the menu's run-out column for a window that is already out.
func ranOutLabel(ranOutAt, now time.Time) string {
	if ranOutAt.IsZero() {
		return "out now"
	}
	return "out since " + runOutSince(ranOutAt, now)
}

// ranOutNote replaces the forecast's leading "out now" with the time the
// tray saw the window run out. Any wait after it is kept.
func ranOutNote(note string, ranOutAt, now time.Time) string {
	if ranOutAt.IsZero() || !strings.HasPrefix(note, "out now") {
		return note
	}
	return "out since " + runOutSince(ranOutAt, now) + strings.TrimPrefix(note, "out now")
}
