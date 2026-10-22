//go:build tray

package tray

import (
	"crypto/sha256"
	"encoding/hex"
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
	// exhaustionDigits is how many hex digits of a hash identify an account.
	exhaustionDigits = 16
)

// exhaustionRecord is what the tray saw of one window in one reset period.
// Every time in it is the tray's own clock, never a provider's timestamp.
type exhaustionRecord struct {
	// Account is the login the readings came from, as an opaque digest. A
	// reading from another account starts a new record.
	Account string `json:"account"`
	// ResetsAt is the first reset seen for the period. Later readings are
	// compared with it, so jitter cannot drift the period along.
	ResetsAt time.Time `json:"resets_at"`
	// LastSeen is the latest reading. A reading not after it is ignored.
	LastSeen time.Time `json:"last_seen"`
	// LastBelow is the latest reading under 100%.
	LastBelow time.Time `json:"last_below"`
	// FirstFull is the first reading at 100% since LastBelow.
	FirstFull time.Time `json:"first_full"`
}

// exhaustionLog says when a quota window ran out, but only when the tray saw
// the change: a reading under 100% followed by one at 100%, with the two
// readings no more than maxGap apart, on one account, in one reset period. A
// window first seen full, or with readings far apart (the machine slept, or
// the tray was off), has no time.
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

// observe records one reading of a window taken at the given time. A source
// with no account, or a window with no reset, is not tracked.
func (l *exhaustionLog) observe(source, account string, w provider.UsageWindow, at time.Time) {
	if account == "" || w.ResetsAt.IsZero() {
		return
	}
	if l.Windows == nil {
		l.Windows = make(map[string]exhaustionRecord)
	}
	key := exhaustionKey(source, w.Name)
	rec, ok := l.Windows[key]
	if ok && !at.After(rec.LastSeen) {
		return
	}
	if !ok || rec.Account != account || !sameReset(rec.ResetsAt, w.ResetsAt) {
		rec = exhaustionRecord{Account: account, ResetsAt: w.ResetsAt}
	}
	rec.LastSeen = at
	if w.Utilization < 100 {
		rec.LastBelow = at
		rec.FirstFull = time.Time{}
	} else if rec.FirstFull.IsZero() {
		rec.FirstFull = at
	}
	l.Windows[key] = rec
}

// ranOutAt returns when the window ran out, if the tray saw it happen within
// maxGap and the transition is not in the future of now. The window must be
// full now, on the same account and in the same reset period as the record.
func (l *exhaustionLog) ranOutAt(source, account string, w provider.UsageWindow, maxGap time.Duration, now time.Time) (time.Time, bool) {
	rec, ok := l.Windows[exhaustionKey(source, w.Name)]
	if !ok || account == "" || rec.Account != account || w.ResetsAt.IsZero() || w.Utilization < 100 || !sameReset(rec.ResetsAt, w.ResetsAt) {
		return time.Time{}, false
	}
	if rec.LastBelow.IsZero() || rec.FirstFull.IsZero() {
		return time.Time{}, false
	}
	gap := rec.FirstFull.Sub(rec.LastBelow)
	if gap <= 0 || gap > maxGap || rec.FirstFull.After(now) {
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

// exhaustionTracker guards an exhaustionLog and the per-run state that says
// which saved records may be shown. Its zero value is an empty log that is
// not saved.
type exhaustionTracker struct {
	mu     sync.Mutex
	path   string
	maxGap time.Duration
	// identities is each configured source's account as of the latest refresh.
	identities map[string]string
	// observed maps a source to the account it was last read as in this run.
	// A record loaded from disk is shown only after this run has read its
	// source under the same account.
	observed map[string]string
	log      exhaustionLog
}

// load replaces the log with the one saved at path, and sets maxGap, the
// longest gap between two readings that still counts as a transition. Until
// the next refresh, no time is shown.
func (t *exhaustionTracker) load(path string, maxGap time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.path = path
	t.maxGap = maxGap
	t.identities = nil
	t.observed = nil
	t.log = loadExhaustionLog(path)
}

// observeFetched records the windows of the results this refresh fetched.
// identities maps each source key to its account; a source without one is not
// tracked. Cached fallbacks are skipped: they are not readings taken now.
// Every reading is stamped with now, the tray's clock.
func (t *exhaustionTracker) observeFetched(results, cached map[string]*provider.UsageData, identities map[string]string, now time.Time, maxGap time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxGap = maxGap
	t.identities = identities
	if t.observed == nil {
		t.observed = make(map[string]string)
	}
	for name, data := range results {
		account := identities[name]
		if account == "" {
			continue
		}
		if _, wasCached := cached[name]; wasCached || !freshReading(data) {
			continue
		}
		for _, w := range data.Windows {
			t.log.observe(name, account, w, now)
		}
		t.observed[name] = account
	}
	if err := t.log.save(t.path, now); err != nil {
		log.Printf("save quota exhaustion log: %v", err)
	}
}

// ranOutAt returns when the window ran out, or zero when the tray cannot say.
// A source answers only if this run has read it under its current account.
func (t *exhaustionTracker) ranOutAt(source string, w provider.UsageWindow, now time.Time) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	account := t.identities[source]
	if account == "" || t.observed[source] != account {
		return time.Time{}
	}
	at, ok := t.log.ranOutAt(source, account, w, t.maxGap, now)
	if !ok {
		return time.Time{}
	}
	return at
}

// sourceIdentities maps each provider's source key to its account identity.
func sourceIdentities(providers []provider.Provider) map[string]string {
	identities := make(map[string]string, len(providers))
	for _, p := range providers {
		if identity := sourceIdentity(p); identity != "" {
			identities[provider.SourceKey(p)] = identity
		}
	}
	return identities
}

// sourceIdentity names the account a source reads. A Claude profile's
// organization identifies it; other sources use their credential revision.
// The raw values are hashed, so the saved file holds no account secrets. An
// empty result means the source cannot be told apart from another account.
func sourceIdentity(p provider.Provider) string {
	if org, ok := p.(interface{ ClaudeOrganizationUUID() string }); ok {
		if uuid := strings.TrimSpace(org.ClaudeOrganizationUUID()); uuid != "" {
			return "org:" + exhaustionDigest(uuid)
		}
	}
	if revision := provider.SourceRevision(p); revision != "" {
		return "rev:" + exhaustionDigest(revision)
	}
	return ""
}

func exhaustionDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:exhaustionDigits]
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
