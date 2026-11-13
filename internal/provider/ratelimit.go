package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/filelock"
)

// RateLimitError is how a provider reports that its upstream answered HTTP
// 429. Providers only report it; FetchSource decides when to call them again.
// A provider that already has a partial reading (for example, one of two
// endpoints answered) returns that reading together with the error, and
// FetchSource keeps it.
type RateLimitError struct {
	// RetryAfter is the server's requested delay, or zero if it sent none.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return "rate limited (HTTP 429)" }

// RateLimitFromResponse returns a *RateLimitError when resp is HTTP 429 and
// nil otherwise. Retry-After may be delay-seconds or an HTTP-date (RFC 9110).
func RateLimitFromResponse(resp *http.Response) error {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	return &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if secs, err := strconv.ParseInt(value, 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		if secs <= 0 {
			return 0
		}
		// Clamp the count before multiplying, so a huge value cannot overflow.
		return time.Duration(min(secs, maxRetryAfterSeconds)) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(backoffNow()), 0)
	}
	return 0
}

// Some usage endpoints are shared with the vendor's own tools and answer 429
// with "retry-after: 0" (Claude does). Honoring that literally would make every
// CLI call retry at once, so after a 429 a source is not called again for a
// floor of 5 minutes, doubling per consecutive 429 up to 1 hour. The floor equals
// the default poll interval, so after a 429 the next scheduled poll is usually
// skipped. A Retry-After from the server is the server's own cooldown, so it wins
// over the local cap and is honored in full. A monthly quota can send days, and a
// retry before its reset is refused again. The state lives on disk because the
// CLI and the tray are separate processes.
const (
	backoffFloor = 5 * time.Minute
	backoffCap   = time.Hour
	// maxRetryAfterSeconds is the largest delay-seconds value a time.Duration
	// holds. A larger value is clamped to it instead of overflowing.
	maxRetryAfterSeconds = math.MaxInt64 / int64(time.Second)
)

var (
	backoffNow = time.Now
	backoffDir = func() (string, error) {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "clawmeter", "backoff"), nil
	}
)

// CredentialRouteCapability is implemented by sources whose credential route
// can change while the source ID stays the same: a repointed config dir, or
// one chosen by the environment. Each route keeps its own backoff. The route
// must not change on a routine token refresh, or a refresh would end a backoff.
type CredentialRouteCapability interface{ CredentialRoute() string }

// CredentialRoute builds the stable route string for a configured credential.
// An absolute ref is a path and is canonicalized (absolute, symlinks resolved)
// so equivalent spellings match; any other ref is an environment variable name
// and is used as written. The route never reads the credential's size, time, or
// content, so a token refresh keeps the same route.
func CredentialRoute(kind, ref string) string {
	ref = strings.TrimSpace(ref)
	if filepath.IsAbs(ref) {
		ref = CanonicalPath(ref)
	}
	return kind + "\x00" + ref
}

// CanonicalPath returns path as an absolute path with symlinks resolved, or ""
// if it cannot be made absolute.
func CanonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs
}

func backoffKey(p Provider) string {
	key := SourceKey(p)
	if route, ok := p.(CredentialRouteCapability); ok {
		key += "\x00" + route.CredentialRoute()
	}
	return key
}

// FetchSource is the one place a source is fetched. It skips sources that are
// in a rate-limit backoff, starts or extends the backoff when the provider
// reports a RateLimitError, and clears it after a clean reading. A rate limit
// is returned as data with Error set, not as an error, so callers fall back to
// the last good reading and mark it stale.
func FetchSource(ctx context.Context, p Provider) (*UsageData, error) {
	key := backoffKey(p)
	// One read both gates the fetch and records the state it was sent under, so a
	// success can tell whether a 429 landed while the request was in flight.
	before, _ := readBackoff(key)
	if backoffNow().Before(before.Until) {
		return rateLimitedData(p, before.Until), nil
	}
	data, err := p.FetchUsage(ctx)
	var limited *RateLimitError
	if errors.As(err, &limited) {
		until := recordRateLimit(key, limited.RetryAfter)
		if data == nil {
			data = rateLimitedData(p, until)
		}
		return data, nil
	}
	if err == nil && data != nil && data.Error == "" {
		clearBackoff(key, before)
	}
	return data, err
}

func rateLimitedData(p Provider, until time.Time) *UsageData {
	return &UsageData{
		Provider: p.Name(), SourceID: SourceID(p), SourceLabel: SourceLabel(p),
		FetchedAt: time.Now(),
		Error:     "rate limited (429), next try " + nextTryLabel(until, backoffNow()),
	}
}

// nextTryLabel says when fetching may resume. A cooldown that ends on a later
// calendar day carries its date, so a multi-day quota wait does not read as a time
// today.
func nextTryLabel(until, now time.Time) string {
	until, now = until.Local(), now.Local()
	if until.Year() == now.Year() && until.YearDay() == now.YearDay() {
		return until.Format("15:04")
	}
	return until.Format("Jan 2 15:04")
}

// backoffState is one source's stored backoff. ID is drawn at random for every
// recorded 429, so it identifies one cooldown. A success clears only the cooldown
// its request saw, and a 429 recorded during the request leaves a new ID behind.
// Zero means no ID: nothing was stored, or a build without IDs wrote the state.
type backoffState struct {
	Until    time.Time `json:"until"`
	Failures int       `json:"failures"`
	ID       uint64    `json:"id"`
}

// backoffPath gives each source and credential route its own state file. Every
// read and write of these files holds the one backoff lock (see lockBackoff).
func backoffPath(key string) (string, error) {
	dir, err := backoffDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key)))), nil
}

// readBackoff returns the stored state for key, and whether there is one. It holds
// the backoff lock, so a read never overlaps a writer's rename.
func readBackoff(key string) (backoffState, bool) {
	unlock, _ := lockBackoff()
	defer unlock()
	return loadBackoff(key)
}

// loadBackoff reads the stored state without taking the lock. Call it only while
// holding the lock. The lock is not reentrant, so calling readBackoff inside it
// would wait out backoffLockWait and then read without the lock.
func loadBackoff(key string) (backoffState, bool) {
	var state backoffState
	path, err := backoffPath(key)
	if err != nil {
		return state, false
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &state) != nil {
		return backoffState{}, false
	}
	return state, true
}

func writeBackoff(key string, state backoffState) {
	path, err := backoffPath(key)
	if err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backoff-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// backoffLockWait bounds how long a read or write waits for the backoff lock. The
// holder keeps it for one small file read and write, so a longer wait means a
// stalled disk or a suspended process.
const backoffLockWait = 2 * time.Second

// lockBackoff takes the backoff lock, one lock file shared by all sources. It orders
// reads and read-modify-writes of backoff state across processes, because the CLI
// and the tray can both record a 429 for one source. It is never held across a
// fetch. ok is false when no lock could be taken: no writable cache directory, no
// file locks on this platform, or a holder that kept the lock past backoffLockWait.
func lockBackoff() (unlock func(), ok bool) {
	dir, err := backoffDir()
	if err != nil || os.MkdirAll(dir, 0o755) != nil {
		return func() {}, false
	}
	release, err := filelock.Acquire(filepath.Join(dir, ".lock"), backoffLockWait)
	if err != nil {
		return func() {}, false
	}
	return release, true
}

// recordRateLimit starts or extends the backoff after a 429 and returns when
// fetching may resume. The read, the new cooldown and the write happen under the
// backoff lock, so an overlapping writer's longer cooldown is not overwritten. If
// no lock can be taken, the write still happens, as it did before the lock existed.
func recordRateLimit(key string, retryAfter time.Duration) time.Time {
	unlock, _ := lockBackoff()
	defer unlock()
	state, _ := loadBackoff(key)
	state.Failures++
	state.ID = rand.Uint64()
	delay := backoffFloor
	for i := 1; i < state.Failures && delay < backoffCap; i++ {
		delay *= 2
	}
	delay = max(min(delay, backoffCap), retryAfter)
	if until := backoffNow().Add(delay); until.After(state.Until) {
		state.Until = until
	}
	writeBackoff(key, state)
	return state.Until
}

// clearBackoff ends a backoff after a clean reading, but only the backoff that was
// in effect when the request was sent. A 429 recorded while the request was in
// flight is newer, and its cooldown still applies after this success, which shows
// only that the request was answered. A zero ID means the request saw no state, or
// saw a state written by a build without IDs. Such a state cannot be matched to this
// request, so a success leaves it alone. If the lock cannot be taken, nothing is
// cleared, and a later clean reading tries again.
func clearBackoff(key string, before backoffState) {
	if before.ID == 0 {
		return
	}
	unlock, ok := lockBackoff()
	defer unlock()
	if !ok {
		return
	}
	now, ok := loadBackoff(key)
	if !ok || now.ID != before.ID {
		return
	}
	if path, err := backoffPath(key); err == nil {
		_ = os.Remove(path)
	}
}
