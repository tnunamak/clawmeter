package resetfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// twoClients returns two clients on one cache path and one clock, as the tray
// and the CLI would be.
func twoClients(t *testing.T, baseURL string, clock *fakeClock) (*Client, *Client) {
	t.Helper()
	path := filepath.Join(t.TempDir(), CacheFileName)
	a := &Client{BaseURL: baseURL, Path: path, Now: clock.Now}
	b := &Client{BaseURL: baseURL, Path: path, Now: clock.Now}
	return a, b
}

func TestSecondProcessWaitsAndReusesFirstResult(t *testing.T) {
	clock := &fakeClock{t: t0}
	body := scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			started <- struct{}{}
			<-release
		}
		okJSON(body, `"v1"`)(n, w, r)
	})
	first, second := twoClients(t, fs.srv.URL, clock)

	firstDone := make(chan Reading, 1)
	go func() {
		r, _ := first.Read(context.Background())
		firstDone <- r
	}()
	<-started

	secondDone := make(chan Reading, 1)
	secondErr := make(chan error, 1)
	go func() {
		r, err := second.Read(context.Background())
		secondDone <- r
		secondErr <- err
	}()
	time.Sleep(100 * time.Millisecond) // second is now waiting on the lock
	close(release)

	r1 := <-firstDone
	r2 := <-secondDone
	if err := <-secondErr; err != nil {
		t.Fatalf("second read: %v", err)
	}
	if n := fs.count(); n != 1 {
		t.Fatalf("requests = %d, want 1: the second process must reuse the first result", n)
	}
	if r1.Announcement == nil || r2.Announcement == nil || r2.Announcement.ID != "evt_1" {
		t.Fatalf("second process saw %+v, want the first result", r2.Announcement)
	}
	if !r2.CheckedAt.Equal(r1.CheckedAt) {
		t.Fatalf("second checked_at = %v, first = %v", r2.CheckedAt, r1.CheckedAt)
	}
}

func TestStaleAnswerNeverOverwritesNewerState(t *testing.T) {
	clock := &fakeClock{t: t0}
	body := scheduledBody("evt_old", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		okJSON(body, `"old"`)(n, w, r)
	})
	client := &Client{BaseURL: fs.srv.URL, Path: filepath.Join(t.TempDir(), CacheFileName), Now: clock.Now}

	done := make(chan Reading, 1)
	go func() {
		r, _ := client.Read(context.Background())
		done <- r
	}()
	<-started

	// A newer successful read lands while our request is in flight. It was
	// written without the lock, as an older binary would, so it is the only way
	// a newer state can appear mid-request.
	newer := stored{
		Version:   storeVersion,
		ETag:      `"new"`,
		CheckedAt: t0.Add(5 * time.Minute),
		Announcement: &Announcement{
			ID:           "evt_new",
			ScheduledFor: time.Date(2026, 10, 23, 18, 0, 0, 0, time.UTC),
			AnnouncedAt:  goodAnnouncedTime(),
			SourceURL:    goodSource,
		},
	}
	writeState(t, client.Path, newer)
	close(release)

	got := <-done
	if got.Announcement == nil || got.Announcement.ID != "evt_new" {
		t.Fatalf("stale answer: reading = %+v, want the newer saved state", got.Announcement)
	}
	onDisk := readState(t, client.Path)
	if onDisk.ETag != `"new"` || onDisk.Announcement == nil || onDisk.Announcement.ID != "evt_new" || !onDisk.CheckedAt.Equal(newer.CheckedAt) {
		t.Fatalf("stale answer overwrote the newer state on disk: %+v", onDisk)
	}
}

func TestBusyLockShowsSavedReadingWithoutRequest(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, okJSON(scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource), `"v1"`))
	path := filepath.Join(t.TempDir(), CacheFileName)
	writeState(t, path, stored{
		Version:      storeVersion,
		CheckedAt:    t0.Add(-time.Hour), // stale: a request would be due
		Announcement: &Announcement{ID: "saved", ScheduledFor: goodScheduledTime(), SourceURL: goodSource},
	})

	// Another process holds the lock.
	holder, err := os.OpenFile(lockPath(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if ok, err := tryLock(holder); err != nil || !ok {
		t.Fatalf("holding the lock: ok=%v err=%v", ok, err)
	}

	client := &Client{BaseURL: fs.srv.URL, Path: path, Now: clock.Now, LockWait: 50 * time.Millisecond}
	reading, err := client.Read(context.Background())
	if err == nil {
		t.Fatal("busy lock: want a diagnostic error")
	}
	if fs.count() != 0 {
		t.Fatalf("busy lock made %d requests, want 0", fs.count())
	}
	if reading.Announcement == nil || reading.Announcement.ID != "saved" {
		t.Fatalf("busy lock must show the saved reading, got %+v", reading.Announcement)
	}
}

// brokenBodyServer answers 200 with a Content-Length it does not honour, or
// with a body cut off by closing the connection. The first response is good.
func TestBrokenBodyKeepsLastGoodAndBacksOff(t *testing.T) {
	good := scheduledBody("evt_1", "scheduled", "regular", goodScheduled, goodAnnounced, goodSource)
	cases := map[string]func(n int, w http.ResponseWriter, r *http.Request){
		"declared length longer than written": func(n int, w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(len(good)+500))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(good[:40]))
		},
		"connection closed mid-body": func(n int, w http.ResponseWriter, r *http.Request) {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nETag: \"v2\"\r\n\r\n%s", len(good)+500, good[:40])
			buf.Flush()
			_ = conn.Close()
		},
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			clock := &fakeClock{t: t0}
			fs := newFeedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
				if n == 1 {
					okJSON(good, `"v1"`)(n, w, r)
					return
				}
				broken(n, w, r)
			})
			client := newTestClient(t, fs.srv.URL, clock)
			if _, err := client.Read(context.Background()); err != nil {
				t.Fatal(err)
			}

			clock.Advance(20 * time.Minute)
			reading, err := client.Read(context.Background())
			if err == nil {
				t.Fatal("broken body: want a diagnostic error")
			}
			if reading.Announcement == nil || reading.Announcement.ID != "evt_1" {
				t.Fatalf("broken body must keep the last good announcement, got %+v", reading.Announcement)
			}
			if !reading.CheckedAt.Equal(t0) {
				t.Fatalf("broken body must not move the last successful read: %v", reading.CheckedAt)
			}
			saved := readState(t, client.Path)
			if saved.Announcement == nil || saved.ETag != `"v1"` {
				t.Fatalf("broken body must keep the saved announcement and ETag: %+v", saved)
			}

			// Backoff: no request for 14 minutes, then the revalidation keeps the ETag.
			clock.Advance(14 * time.Minute)
			_, _ = client.Read(context.Background())
			if fs.count() != 2 {
				t.Fatalf("requests during backoff = %d, want 2", fs.count())
			}
			clock.Advance(time.Minute)
			_, _ = client.Read(context.Background())
			if fs.count() != 3 {
				t.Fatalf("requests after backoff = %d, want 3", fs.count())
			}
			if fs.seenINM[2] != `"v1"` {
				t.Fatalf("revalidation after a broken body sent If-None-Match %q, want \"v1\"", fs.seenINM[2])
			}
		})
	}
}

func TestRetryAfterLargeValuesAreClamped(t *testing.T) {
	cases := map[string]time.Duration{
		"9223372036854775807":     LastGoodLimit, // max int64: multiplied, it would overflow
		"9223372036854775806":     LastGoodLimit,
		"9223372036":              LastGoodLimit,
		"18446744073709551616":    LastGoodLimit, // beyond int64 entirely
		"99999999999999999999999": LastGoodLimit,
		"7200":                    LastGoodLimit, // exactly the cap
		"7199":                    7199 * time.Second,
		"120":                     2 * time.Minute,
		"0":                       failureBackoff,
		"-9223372036854775808":    failureBackoff,
	}
	for header, want := range cases {
		if got := retryAfter(header, t0); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestHugeRetryAfterDoesNotDisableTheFeed(t *testing.T) {
	clock := &fakeClock{t: t0}
	fs := newFeedServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "9223372036854775807")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	client := newTestClient(t, fs.srv.URL, clock)
	_, _ = client.Read(context.Background())
	clock.Advance(LastGoodLimit - time.Second)
	_, _ = client.Read(context.Background())
	if fs.count() != 1 {
		t.Fatalf("requests before the cap = %d, want 1", fs.count())
	}
	clock.Advance(2 * time.Second)
	_, _ = client.Read(context.Background())
	if fs.count() != 2 {
		t.Fatalf("requests after the cap = %d, want 2: a huge Retry-After must not switch the feed off", fs.count())
	}
}

func goodScheduledTime() time.Time {
	t, _ := time.Parse(time.RFC3339, goodScheduled)
	return t
}

func goodAnnouncedTime() time.Time {
	t, _ := time.Parse(time.RFC3339, goodAnnounced)
	return t
}

func writeState(t *testing.T, path string, st stored) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	st.Version = storeVersion
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readState(t *testing.T, path string) stored {
	t.Helper()
	st, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
