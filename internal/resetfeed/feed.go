// Package resetfeed reads the unofficial codex-resets.com status feed and
// decides which announced Codex resets bear on which accounts. The feed is
// third party and carries no account data; the only request is a GET of its
// status endpoint, made only when the user has opted in.
package resetfeed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	// DefaultBaseURL is the feed origin. Tests inject another base URL.
	DefaultBaseURL = "https://codex-resets.com"
	statusPath     = "/api/v1/status"
	// CacheFileName sits in the Clawmeter cache directory, shared by every
	// process that reads the feed.
	CacheFileName = "codex-reset-feed.json"

	requestTimeout  = 5 * time.Second
	maxBodyBytes    = 1 << 20
	revalidateAfter = 15 * time.Minute
	failureBackoff  = 15 * time.Minute
	// LastGoodLimit is how long a successful read keeps an announcement
	// visible while the feed is unreachable.
	LastGoodLimit = 2 * time.Hour
	// PastLimit is how far a scheduled time may be in the past and still show.
	PastLimit = 6 * time.Hour
)

// Announcement is the feed's current scheduled regular reset, as validated.
type Announcement struct {
	ID           string    `json:"id,omitempty"`
	ScheduledFor time.Time `json:"scheduled_for"`
	AnnouncedAt  time.Time `json:"announced_at"`
	SourceURL    string    `json:"source_url"`
}

// Reading is the last state of the feed that Clawmeter holds.
type Reading struct {
	// CheckedAt is the last successful read: a 200 or a 304 response.
	CheckedAt time.Time
	// Announcement is nil when the feed shows no qualifying scheduled reset.
	Announcement *Announcement
}

// stored is the on-disk form in the cache directory. It holds no account
// data and no raw response.
type stored struct {
	Version       int           `json:"version"`
	ETag          string        `json:"etag,omitempty"`
	CheckedAt     time.Time     `json:"checked_at,omitempty"`
	NextRequestAt time.Time     `json:"next_request_at,omitempty"`
	Announcement  *Announcement `json:"announcement,omitempty"`
}

const storeVersion = 1

// Client reads the feed. Zero fields take the production defaults.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Path is the cache file. It is required; use Default for the real one.
	Path string
	Now  func() time.Time
	// LockWait bounds how long Read waits for the cross-process lock before
	// it returns the saved reading without requesting. Zero means 10 seconds.
	LockWait time.Duration
}

// Default returns a client for the production feed and the Clawmeter cache.
func Default() (*Client, error) {
	path, err := cache.Path(CacheFileName)
	if err != nil {
		return nil, err
	}
	return &Client{BaseURL: DefaultBaseURL, Path: path}, nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

const (
	defaultLockWait = 10 * time.Second
	lockPoll        = 25 * time.Millisecond
)

// lockPath is the lock file beside the cache file: codex-reset-feed.lock.
func lockPath(cachePath string) string {
	return strings.TrimSuffix(cachePath, filepath.Ext(cachePath)) + ".lock"
}

// lock takes the exclusive cross-process lock that serializes reads, requests
// and saves. It returns (nil, nil) when the lock is still held after
// LockWait. The lock is held by an open file, and closing the file releases
// it on every platform.
func (c *Client) lock() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create feed cache dir: %w", err)
	}
	f, err := os.OpenFile(lockPath(c.Path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	wait := c.LockWait
	if wait <= 0 {
		wait = defaultLockWait
	}
	// The wait uses the real clock: a test clock that does not advance must
	// not turn a held lock into an endless loop.
	deadline := time.Now().Add(wait)
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return f, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, nil
		}
		time.Sleep(lockPoll)
	}
}

func unlock(f *os.File) {
	f.Close()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: requestTimeout}
}

// Wanted reports whether the feed should be read: the user opted in and at
// least one Codex (openai) source is configured.
func Wanted(enabled bool, configured []provider.Provider) bool {
	if !enabled {
		return false
	}
	for _, p := range configured {
		if provider.FamilyName(p) == "openai" {
			return true
		}
	}
	return false
}

// Read returns the feed reading, making at most one request. It takes the
// cross-process lock first, so a second process waits and then reuses the
// first one's result instead of asking the feed again. It reuses a read
// younger than 15 minutes, revalidates an older one with If-None-Match, and
// honours the backoff after a failure. If the lock cannot be taken within
// LockWait, it returns what is saved and makes no request. A failed request
// returns the last good reading with the error for diagnostics only; callers
// show nothing special to the user.
func (c *Client) Read(ctx context.Context) (Reading, error) {
	if c.Path == "" {
		return Reading{}, errors.New("no feed cache path")
	}
	lock, err := c.lock()
	if err != nil {
		return c.savedReading(), fmt.Errorf("feed lock: %w", err)
	}
	if lock == nil {
		return c.savedReading(), errors.New("feed cache is busy; showing the saved reading")
	}
	defer unlock(lock)

	now := c.now()
	// Reload after taking the lock: another process may have just saved.
	st, _ := load(c.Path)
	reading := st.reading()

	if !st.CheckedAt.IsZero() && now.Sub(st.CheckedAt) >= 0 && now.Sub(st.CheckedAt) < revalidateAfter {
		return reading, nil
	}
	if now.Before(st.NextRequestAt) {
		return reading, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+statusPath, nil)
	if err != nil {
		return reading, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "clawmeter")
	if st.ETag != "" {
		req.Header.Set("If-None-Match", st.ETag)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return c.commit(backoff(st, now), fmt.Errorf("feed request failed: %w", err))
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified && st.ETag != "":
		st.CheckedAt = now
		st.NextRequestAt = time.Time{}
		return c.commit(st, nil)
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
		if err != nil {
			// A broken or truncated body says nothing about the feed's state:
			// keep the last good reading and back off like any failure.
			return c.commit(backoff(st, now), fmt.Errorf("feed body: %w", err))
		}
		if len(body) > maxBodyBytes {
			return c.commit(unusable(st, now), errors.New("feed body exceeds 1 MiB"))
		}
		announcement, ok := parseStatus(body)
		if !ok {
			return c.commit(unusable(st, now), errors.New("feed body has an unexpected shape"))
		}
		st.Announcement = announcement
		st.ETag = resp.Header.Get("ETag")
		st.CheckedAt = now
		st.NextRequestAt = time.Time{}
		return c.commit(st, nil)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		st.NextRequestAt = now.Add(retryAfter(resp.Header.Get("Retry-After"), now))
		return c.commit(st, fmt.Errorf("feed answered %d", resp.StatusCode))
	default:
		return c.commit(backoff(st, now), fmt.Errorf("feed answered %d", resp.StatusCode))
	}
}

// savedReading is what is on disk, without any request. It is used when the
// lock cannot be taken in time.
func (c *Client) savedReading() Reading {
	st, _ := load(c.Path)
	return st.reading()
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

// backoff keeps the saved reading and waits before the next request.
func backoff(st stored, now time.Time) stored {
	st.NextRequestAt = now.Add(failureBackoff)
	return st
}

// unusable records a response whose shape Clawmeter does not understand, or a
// body over the size limit: it shows nothing, is not counted as a successful
// read, and backs off.
func unusable(st stored, now time.Time) stored {
	st.Announcement = nil
	st.ETag = ""
	st.NextRequestAt = now.Add(failureBackoff)
	return st
}

// retryAfter reads a Retry-After header in seconds or as an HTTP date. A
// missing, zero, or past value falls back to the default backoff. A wait
// longer than LastGoodLimit is cut to it, so a bad header cannot switch the
// feed off for good. Seconds are clamped before they become a duration, so a
// huge value cannot overflow.
func retryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if isDigits(header) {
		secs, err := strconv.ParseInt(header, 10, 64)
		if err != nil {
			// Out of int64 range: far beyond any usable wait.
			return LastGoodLimit
		}
		if secs <= 0 {
			return failureBackoff
		}
		if secs >= int64(LastGoodLimit/time.Second) {
			return LastGoodLimit
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		if d := at.Sub(now); d > 0 {
			return min(d, LastGoodLimit)
		}
	}
	return failureBackoff
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (st stored) reading() Reading {
	return Reading{CheckedAt: st.CheckedAt, Announcement: st.Announcement}
}

// statusBody decodes only the fields Clawmeter uses. A field of the wrong
// type fails the whole body; a missing or null field yields no announcement.
type statusBody struct {
	Data struct {
		ScheduledReset *scheduledReset `json:"scheduled_reset"`
	} `json:"data"`
}

type scheduledReset struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ResetType    string `json:"reset_type"`
	AnnouncedAt  string `json:"announced_at"`
	ScheduledFor string `json:"scheduled_for"`
	Source       struct {
		URL string `json:"url"`
	} `json:"source"`
}

// parseStatus returns the announcement a status body carries. ok is false
// when the body is not the expected JSON; a well-formed body that does not
// qualify returns (nil, true).
func parseStatus(body []byte) (*Announcement, bool) {
	var parsed statusBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false
	}
	sr := parsed.Data.ScheduledReset
	if sr == nil || sr.Status != "scheduled" || sr.ResetType != "regular" {
		return nil, true
	}
	scheduledFor, err := time.Parse(time.RFC3339, sr.ScheduledFor)
	if err != nil {
		return nil, true
	}
	announcedAt, err := time.Parse(time.RFC3339, sr.AnnouncedAt)
	if err != nil {
		return nil, true
	}
	source, err := url.Parse(sr.Source.URL)
	if err != nil || source.Scheme != "https" || source.Host == "" {
		return nil, true
	}
	return &Announcement{
		ID:           sr.ID,
		ScheduledFor: scheduledFor,
		AnnouncedAt:  announcedAt,
		SourceURL:    sr.Source.URL,
	}, true
}

// load returns the stored state. A missing or unreadable file is an empty
// state; the feed is supplemental, so this never fails the caller.
func load(path string) (stored, error) {
	var st stored
	if path == "" {
		return st, errors.New("no feed cache path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return stored{}, err
	}
	if st.Version != storeVersion {
		return stored{}, fmt.Errorf("feed cache version %d is not %d", st.Version, storeVersion)
	}
	return st, nil
}

// commit writes the state with a temp file and rename, while the lock is
// held. A stale answer never overwrites a newer successful read: if the saved
// state has a later CheckedAt, it is kept and returned instead. The error is
// cause, or the write error if that is what failed.
func (c *Client) commit(st stored, cause error) (Reading, error) {
	current, _ := load(c.Path)
	if current.CheckedAt.After(st.CheckedAt) {
		return current.reading(), cause
	}
	st.Version = storeVersion
	if err := writeAtomic(c.Path, st); err != nil {
		if cause != nil {
			return st.reading(), fmt.Errorf("%w (and %v)", cause, err)
		}
		return st.reading(), err
	}
	return st.reading(), cause
}

// writeAtomic writes the state with a temp file and rename, so a concurrent
// reader sees either the old or the new file.
func writeAtomic(path string, st stored) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create feed cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".codex-reset-feed-*")
	if err != nil {
		return fmt.Errorf("create feed temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect feed temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write feed temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close feed temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace feed cache: %w", err)
	}
	return nil
}
