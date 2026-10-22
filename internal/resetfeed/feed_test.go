package resetfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// fakeClock is the injected wall clock for the client.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// scheduledBody is a feed status body with one scheduled reset.
func scheduledBody(id, status, resetType, scheduledFor, announcedAt, sourceURL string) string {
	return fmt.Sprintf(`{"data":{"scheduled_reset":{"id":%q,"status":%q,"reset_type":%q,"announced_at":%q,"scheduled_for":%q,"text":"lands tomorrow before 10am PST","source":{"type":"x","author":"someone","url":%q}},"history":[]},"meta":{"generated_at":"2026-10-22T12:00:00Z"}}`,
		id, status, resetType, announcedAt, scheduledFor, sourceURL)
}

const (
	goodSource    = "https://x.com/example/status/1"
	goodScheduled = "2026-10-23T17:00:00Z"
	goodAnnounced = "2026-10-22T11:30:00Z"
)

// feedServer serves responses in order and counts requests.
type feedServer struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	requests  int
	seenPaths []string
	seenINM   []string
	respond   func(n int, w http.ResponseWriter, r *http.Request)
}

func newFeedServer(t *testing.T, respond func(n int, w http.ResponseWriter, r *http.Request)) *feedServer {
	t.Helper()
	fs := &feedServer{t: t, respond: respond}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.requests++
		n := fs.requests
		fs.seenPaths = append(fs.seenPaths, r.Method+" "+r.URL.Path)
		fs.seenINM = append(fs.seenINM, r.Header.Get("If-None-Match"))
		fs.mu.Unlock()
		fs.respond(n, w, r)
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *feedServer) count() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.requests
}

func okJSON(body string, etag string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func newTestClient(t *testing.T, baseURL string, clock *fakeClock) *Client {
	t.Helper()
	return &Client{
		BaseURL: baseURL,
		Path:    filepath.Join(t.TempDir(), CacheFileName),
		Now:     clock.Now,
	}
}

func TestUpcomingScheduledResetIsStored(t *testing.T) {
	clock := &fakeClock{t: t0}
	body := scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	fs := newFeedServer(t, okJSON(body, `"v1"`))
	client := newTestClient(t, fs.srv.URL, clock)

	reading, err := client.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if reading.Announcement == nil {
		t.Fatal("announcement = nil, want the scheduled reset")
	}
	a := reading.Announcement
	if a.ID != "evt_1" || a.SourceURL != goodSource {
		t.Fatalf("announcement = %+v", a)
	}
	if !a.ScheduledFor.Equal(time.Date(2026, 10, 23, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("scheduled_for = %v", a.ScheduledFor)
	}
	if !reading.CheckedAt.Equal(t0) {
		t.Fatalf("checked_at = %v, want %v", reading.CheckedAt, t0)
	}
	if got := fs.seenPaths[0]; got != "GET /api/v1/status" {
		t.Fatalf("request = %q, want only GET /api/v1/status", got)
	}
}

func TestNonQualifyingBodiesYieldNoAnnouncement(t *testing.T) {
	cases := map[string]string{
		"banked type":       scheduledBody("e", "scheduled", "banked", goodScheduled, goodAnnounced, goodSource),
		"status not sched.": scheduledBody("e", "executed", "regular", goodScheduled, goodAnnounced, goodSource),
		"missing sched.":    `{"data":{"scheduled_reset":{"id":"e","status":"scheduled","reset_type":"regular","announced_at":"2026-10-22T11:30:00Z","source":{"url":"https://x.com/a"}}}}`,
		"null reset":        `{"data":{"scheduled_reset":null}}`,
		"http source":       scheduledBody("e", "scheduled", "regular", goodScheduled, goodAnnounced, "http://x.com/a"),
		"bad announced_at":  scheduledBody("e", "scheduled", "regular", goodScheduled, "yesterday", goodSource),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			clock := &fakeClock{t: t0}
			fs := newFeedServer(t, okJSON(body, ""))
			client := newTestClient(t, fs.srv.URL, clock)
			reading, err := client.Read(context.Background())
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if reading.Announcement != nil {
				t.Fatalf("announcement = %+v, want none", reading.Announcement)
			}
			if !reading.CheckedAt.Equal(t0) {
				t.Fatalf("a well-formed answer is a successful read; checked_at = %v", reading.CheckedAt)
			}
		})
	}
}

func TestMalformedJSONShowsNothingAndDoesNotCountAsRead(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			okJSON(scheduledBody("e", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource), `"v1"`)(n, w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data": not json`))
	})
	client := newTestClient(t, fs.srv.URL, clock)
	if _, err := client.Read(context.Background()); err != nil {
		t.Fatalf("first read: %v", err)
	}

	clock.Advance(20 * time.Minute)
	reading, err := client.Read(context.Background())
	if err == nil {
		t.Fatal("malformed body: want a diagnostic error")
	}
	if reading.Announcement != nil {
		t.Fatalf("malformed body must show nothing, got %+v", reading.Announcement)
	}
	if !reading.CheckedAt.Equal(t0) {
		t.Fatalf("malformed body must not count as a successful read; checked_at = %v", reading.CheckedAt)
	}
	// The unusable answer backs off 15 minutes, so the next call makes no request.
	clock.Advance(time.Minute)
	if _, _ = client.Read(context.Background()); fs.count() != 2 {
		t.Fatalf("requests = %d, want 2 (backoff after an unusable body)", fs.count())
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	clock := &fakeClock{t: t0}
	// Valid JSON with a qualifying announcement and a 1 MiB + padding field.
	padding := strings.Repeat("a", maxBodyBytes+10)
	body := fmt.Sprintf(`{"pad":%q,"data":{"scheduled_reset":{"id":"e","status":"scheduled","reset_type":"regular","announced_at":%q,"scheduled_for":%q,"source":{"url":%q}}}}`,
		padding, goodAnnounced, goodScheduled, goodSource)
	fs := newFeedServer(t, okJSON(body, `"big"`))
	client := newTestClient(t, fs.srv.URL, clock)

	reading, err := client.Read(context.Background())
	if err == nil {
		t.Fatal("oversized body: want an error")
	}
	if reading.Announcement != nil {
		t.Fatalf("oversized body must not yield an announcement: %+v", reading.Announcement)
	}
}

func TestRevalidatesWithIfNoneMatchAndKeepsBody(t *testing.T) {
	clock := &fakeClock{t: t0}
	body := scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			okJSON(body, `"v1"`)(n, w, r)
			return
		}
		if r.Header.Get("If-None-Match") != `"v1"` {
			t.Errorf("If-None-Match = %q, want \"v1\"", r.Header.Get("If-None-Match"))
		}
		w.WriteHeader(http.StatusNotModified)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	if _, err := client.Read(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Within 15 minutes the read is reused without a request.
	clock.Advance(14 * time.Minute)
	if _, err := client.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fs.count() != 1 {
		t.Fatalf("requests within 15 minutes = %d, want 1", fs.count())
	}

	// Past 15 minutes it revalidates; 304 keeps the body and moves the read time.
	clock.Advance(2 * time.Minute)
	reading, err := client.Read(context.Background())
	if err != nil {
		t.Fatalf("304 read: %v", err)
	}
	if fs.count() != 2 {
		t.Fatalf("requests = %d, want 2", fs.count())
	}
	if reading.Announcement == nil || reading.Announcement.ID != "evt_1" {
		t.Fatalf("304 must keep the stored announcement, got %+v", reading.Announcement)
	}
	if !reading.CheckedAt.Equal(clock.Now()) {
		t.Fatalf("304 must update the last successful read: checked_at = %v", reading.CheckedAt)
	}
}

func TestEtagChangeWithSameEventIsNotANewEvent(t *testing.T) {
	clock := &fakeClock{t: t0}
	body := scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		etag := `"v1"`
		if n > 1 {
			etag = `"v2"`
		}
		okJSON(body, etag)(n, w, r)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	first, err := client.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(16 * time.Minute)
	second, err := client.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fs.seenINM[1] != `"v1"` {
		t.Fatalf("revalidation sent If-None-Match %q, want \"v1\"", fs.seenINM[1])
	}
	if *first.Announcement != *second.Announcement {
		t.Fatalf("same scheduled_reset with a new ETag changed the announcement: %+v vs %+v", first.Announcement, second.Announcement)
	}
}

func TestRateLimitHonoursRetryAfterSeconds(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	if _, err := client.Read(context.Background()); err == nil {
		t.Fatal("429: want a diagnostic error")
	}
	clock.Advance(119 * time.Second)
	_, _ = client.Read(context.Background())
	if fs.count() != 1 {
		t.Fatalf("requests before Retry-After elapsed = %d, want 1", fs.count())
	}
	clock.Advance(2 * time.Second)
	_, _ = client.Read(context.Background())
	if fs.count() != 2 {
		t.Fatalf("requests after Retry-After = %d, want 2", fs.count())
	}
}

func TestServiceUnavailableHonoursRetryAfterDate(t *testing.T) {
	clock := &fakeClock{t: t0}
	retryAt := t0.Add(10 * time.Minute).UTC().Format(http.TimeFormat)
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", retryAt)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	_, _ = client.Read(context.Background())
	clock.Advance(9 * time.Minute)
	_, _ = client.Read(context.Background())
	if fs.count() != 1 {
		t.Fatalf("requests before the Retry-After date = %d, want 1", fs.count())
	}
	clock.Advance(time.Minute + time.Second)
	_, _ = client.Read(context.Background())
	if fs.count() != 2 {
		t.Fatalf("requests after the Retry-After date = %d, want 2", fs.count())
	}
}

func TestFailureBacksOffFifteenMinutes(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	_, _ = client.Read(context.Background())
	clock.Advance(14 * time.Minute)
	_, _ = client.Read(context.Background())
	if fs.count() != 1 {
		t.Fatalf("requests during backoff = %d, want 1", fs.count())
	}
	clock.Advance(time.Minute)
	_, _ = client.Read(context.Background())
	if fs.count() != 2 {
		t.Fatalf("requests after backoff = %d, want 2", fs.count())
	}
}

func TestRetryAfterDefaultsWhenMissingOrInvalid(t *testing.T) {
	for _, header := range []string{"", "soon", "0", "-5", "Mon, 01 Jan 1990 00:00:00 GMT"} {
		if got := retryAfter(header, t0); got != failureBackoff {
			t.Errorf("retryAfter(%q) = %v, want %v", header, got, failureBackoff)
		}
	}
}

func TestNetworkErrorKeepsLastGoodReading(t *testing.T) {
	clock := &fakeClock{t: t0}
	body := scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			okJSON(body, `"v1"`)(n, w, r)
			return
		}
		// Hijack and close the connection to simulate the network going away.
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	})
	client := newTestClient(t, fs.srv.URL, clock)
	if _, err := client.Read(context.Background()); err != nil {
		t.Fatal(err)
	}

	clock.Advance(20 * time.Minute)
	reading, err := client.Read(context.Background())
	if err == nil {
		t.Fatal("network error: want a diagnostic error")
	}
	if reading.Announcement == nil || reading.Announcement.ID != "evt_1" {
		t.Fatalf("network error must keep the last good announcement, got %+v", reading.Announcement)
	}
	if !reading.CheckedAt.Equal(t0) {
		t.Fatalf("a failed request must not move the last successful read: %v", reading.CheckedAt)
	}
}

func TestStoredStateIsPrivateAndAtomic(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, okJSON(scheduledBody("e", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource), `"v1"`))
	client := newTestClient(t, fs.srv.URL, clock)
	if _, err := client.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(client.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode = %o, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(client.Path)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for key := range st {
		switch key {
		case "version", "etag", "checked_at", "next_request_at", "announcement":
		default:
			t.Fatalf("stored state has unexpected key %q", key)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(client.Path))
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if len(entries) != 2 || !names[CacheFileName] || !names["codex-reset-feed.lock"] {
		t.Fatalf("cache dir entries = %v, want the state file and its lock only", names)
	}
}

func TestNoCachePathMakesNoRequest(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, okJSON("{}", ""))
	client := &Client{BaseURL: fs.srv.URL, Now: clock.Now}
	if _, err := client.Read(context.Background()); err == nil {
		t.Fatal("empty cache path: want an error")
	}
	if fs.count() != 0 {
		t.Fatalf("requests = %d, want 0", fs.count())
	}
}

func TestWantedNeedsOptInAndCodexSource(t *testing.T) {
	codex := fakeProvider{name: "openai"}
	claude := fakeProvider{name: "claude"}
	if Wanted(false, []provider.Provider{codex}) {
		t.Fatal("setting off must not want the feed")
	}
	if Wanted(true, []provider.Provider{claude}) {
		t.Fatal("no Codex source must not want the feed")
	}
	if !Wanted(true, []provider.Provider{claude, codex}) {
		t.Fatal("opt-in with a Codex source must want the feed")
	}
}

type fakeProvider struct{ name string }

func (f fakeProvider) Name() string        { return f.name }
func (f fakeProvider) DisplayName() string { return f.name }
func (f fakeProvider) Description() string { return "" }
func (f fakeProvider) DashboardURL() string {
	return ""
}
func (f fakeProvider) IsConfigured() bool { return true }
func (f fakeProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return nil, nil
}

func TestNoneAnnouncementRevalidatesWithETag(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			okJSON(`{"data":{"scheduled_reset":null}}`, `"none"`)(n, w, r)
			return
		}
		if r.Header.Get("If-None-Match") != `"none"` {
			t.Errorf("If-None-Match = %q, want \"none\"", r.Header.Get("If-None-Match"))
		}
		w.WriteHeader(http.StatusNotModified)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	if _, err := client.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(16 * time.Minute)
	reading, err := client.Read(context.Background())
	if err != nil {
		t.Fatalf("304 on a none reading: %v", err)
	}
	if reading.Announcement != nil || !reading.CheckedAt.Equal(clock.Now()) {
		t.Fatalf("304 on a none reading: %+v", reading)
	}
}

func TestALongRetryAfterIsCut(t *testing.T) {
	now := time.Now()
	if got := retryAfter("999999999", now); got != LastGoodLimit {
		t.Fatalf("retryAfter = %v, want %v", got, LastGoodLimit)
	}
}
