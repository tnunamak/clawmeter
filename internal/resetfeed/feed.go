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

// Read returns the feed reading, making at most one request. It reuses a
// read younger than 15 minutes, revalidates an older one with If-None-Match,
// and honours the backoff after a failure. A failed request returns the last
// good reading with the error for diagnostics only; callers show nothing
// special to the user.
func (c *Client) Read(ctx context.Context) (Reading, error) {
	if c.Path == "" {
		return Reading{}, errors.New("no feed cache path")
	}
	now := c.now()
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
		st.NextRequestAt = now.Add(failureBackoff)
		return reading, c.save(st, fmt.Errorf("feed request failed: %w", err))
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified && st.ETag != "":
		st.CheckedAt = now
		st.NextRequestAt = time.Time{}
		return st.reading(), c.save(st, nil)
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
		if err != nil {
			return c.saveUnusable(st, now, fmt.Errorf("feed body: %w", err))
		}
		if len(body) > maxBodyBytes {
			return c.saveUnusable(st, now, errors.New("feed body exceeds 1 MiB"))
		}
		announcement, ok := parseStatus(body)
		if !ok {
			return c.saveUnusable(st, now, errors.New("feed body has an unexpected shape"))
		}
		st.Announcement = announcement
		st.ETag = resp.Header.Get("ETag")
		st.CheckedAt = now
		st.NextRequestAt = time.Time{}
		return st.reading(), c.save(st, nil)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		st.NextRequestAt = now.Add(retryAfter(resp.Header.Get("Retry-After"), now))
		return reading, c.save(st, fmt.Errorf("feed answered %d", resp.StatusCode))
	default:
		st.NextRequestAt = now.Add(failureBackoff)
		return reading, c.save(st, fmt.Errorf("feed answered %d", resp.StatusCode))
	}
}

func (c *Client) saveUnusable(st stored, now time.Time, cause error) (Reading, error) {
	next := unusable(st, now)
	return next.reading(), c.save(next, cause)
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

// unusable records a response whose shape Clawmeter does not understand: it
// shows nothing, is not counted as a successful read, and backs off.
func unusable(st stored, now time.Time) stored {
	st.Announcement = nil
	st.ETag = ""
	st.NextRequestAt = now.Add(failureBackoff)
	return st
}

// retryAfter reads a Retry-After header in seconds or as an HTTP date. A
// missing, zero, or past value falls back to the default backoff.
func retryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if secs, err := strconv.Atoi(header); err == nil {
		if secs > 0 {
			return time.Duration(secs) * time.Second
		}
		return failureBackoff
	}
	if at, err := http.ParseTime(header); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return failureBackoff
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

// save writes the state with a temp file and rename, so a concurrent reader
// sees either the old or the new file. Concurrent writers are last-wins.
func (c *Client) save(st stored, cause error) error {
	st.Version = storeVersion
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.Path)
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
	if err := os.Rename(tmp.Name(), c.Path); err != nil {
		return fmt.Errorf("replace feed cache: %w", err)
	}
	return cause
}
