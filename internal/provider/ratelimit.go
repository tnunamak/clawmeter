package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	if secs, err := strconv.Atoi(value); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(backoffNow()), 0)
	}
	return 0
}

// Some usage endpoints are shared with the vendor's own tools and answer 429
// with "retry-after: 0" (Claude does). Honoring that literally would make every
// CLI call retry at once, so after a 429 a source is not called again for a
// floor of 5 minutes, doubling per consecutive 429 up to 1 hour. A longer
// Retry-After wins, up to the same cap. The state lives on disk because the CLI
// and the tray are separate processes.
const (
	backoffFloor = 5 * time.Minute
	backoffCap   = time.Hour
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
	if until, ok := backoffUntil(key); ok {
		return rateLimitedData(p, until), nil
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
		clearBackoff(key)
	}
	return data, err
}

func rateLimitedData(p Provider, until time.Time) *UsageData {
	return &UsageData{
		Provider: p.Name(), SourceID: SourceID(p), SourceLabel: SourceLabel(p),
		FetchedAt: time.Now(),
		Error:     "rate limited (429), next try " + until.Local().Format("15:04"),
	}
}

type backoffState struct {
	Until    time.Time `json:"until"`
	Failures int       `json:"failures"`
}

// backoffPath gives each source its own file, so concurrent 429s from
// different sources (or from the CLI and the tray) never rewrite each other's
// state. Two writers for the same source compute near-identical state, so last
// writer wins is acceptable there.
func backoffPath(key string) (string, error) {
	dir, err := backoffDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key)))), nil
}

func readBackoff(key string) (backoffState, bool) {
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

// backoffUntil returns the time before which the source must not be fetched.
func backoffUntil(key string) (time.Time, bool) {
	state, ok := readBackoff(key)
	if !ok || !backoffNow().Before(state.Until) {
		return time.Time{}, false
	}
	return state.Until, true
}

// recordRateLimit starts or extends the backoff after a 429 and returns when
// fetching may resume.
func recordRateLimit(key string, retryAfter time.Duration) time.Time {
	state, _ := readBackoff(key)
	state.Failures++
	delay := backoffFloor
	for i := 1; i < state.Failures && delay < backoffCap; i++ {
		delay *= 2
	}
	delay = min(max(delay, retryAfter), backoffCap)
	if until := backoffNow().Add(delay); until.After(state.Until) {
		state.Until = until
	}
	writeBackoff(key, state)
	return state.Until
}

func clearBackoff(key string) {
	if path, err := backoffPath(key); err == nil {
		_ = os.Remove(path)
	}
}
