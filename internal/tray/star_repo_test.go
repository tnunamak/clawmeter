package tray

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGH answers the gh calls starRepo makes. Calls it does not know fail, so
// a test sees an unexpected call.
type fakeGH struct {
	missing   bool
	authErr   error
	getErr    error
	getStderr []byte
	putErr    error
	putStderr []byte
	calls     []string
}

func (f *fakeGH) lookPath(string) (string, error) {
	if f.missing {
		return "", exec.ErrNotFound
	}
	return "/usr/bin/gh", nil
}

func (f *fakeGH) run(_ context.Context, args ...string) ([]byte, []byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	switch key {
	case "auth status":
		return nil, nil, f.authErr
	case "api user/starred/tnunamak/clawmeter":
		return nil, f.getStderr, f.getErr
	case "api -X PUT user/starred/tnunamak/clawmeter":
		return nil, f.putStderr, f.putErr
	}
	return nil, nil, errors.New("unexpected gh " + key)
}

var errExit1 = errors.New("exit status 1")

var notStarred = []byte("gh: Not Found (HTTP 404)\n")

// newTestStarRepo wires a starRepo to fake gh and records the visibility it
// is given. The shown value starts as the item's default, hidden.
func newTestStarRepo(f *fakeGH) (*starRepo, *[]bool) {
	var shown []bool
	r := newStarRepo(func(s bool) { shown = append(shown, s) })
	r.run = f.run
	r.lookPath = f.lookPath
	return r, &shown
}

func lastShown(t *testing.T, shown *[]bool) bool {
	t.Helper()
	if len(*shown) == 0 {
		t.Fatal("setShown was never called")
	}
	return (*shown)[len(*shown)-1]
}

func TestStarRepoHidesWhenGHMissing(t *testing.T) {
	f := &fakeGH{missing: true}
	r, shown := newTestStarRepo(f)
	r.Check()
	if lastShown(t, shown) {
		t.Fatal("item shown with gh missing")
	}
	if len(f.calls) != 0 {
		t.Fatalf("gh ran without being found: %v", f.calls)
	}
}

func TestStarRepoHidesWhenNotLoggedIn(t *testing.T) {
	f := &fakeGH{authErr: errExit1, getErr: errExit1, getStderr: notStarred}
	r, shown := newTestStarRepo(f)
	r.Check()
	if lastShown(t, shown) {
		t.Fatal("item shown when gh auth status failed")
	}
	if len(f.calls) != 1 || f.calls[0] != "auth status" {
		t.Fatalf("expected only auth status before giving up, got %v", f.calls)
	}
}

func TestStarRepoHidesWhenAlreadyStarred(t *testing.T) {
	f := &fakeGH{} // starred lookup exits 0 (204)
	r, shown := newTestStarRepo(f)
	r.Check()
	if lastShown(t, shown) {
		t.Fatal("item shown for an already starred repository")
	}
}

func TestStarRepoStarClickHidesItem(t *testing.T) {
	f := &fakeGH{getErr: errExit1, getStderr: notStarred}
	r, shown := newTestStarRepo(f)
	r.Check()
	if !lastShown(t, shown) {
		t.Fatal("item hidden for a repository that is not starred")
	}

	if err := r.Star(); err != nil {
		t.Fatalf("Star: %v", err)
	}
	if lastShown(t, shown) {
		t.Fatal("item still shown after a successful star")
	}
	if got := f.calls[len(f.calls)-1]; got != "api -X PUT user/starred/tnunamak/clawmeter" {
		t.Fatalf("star call = %q", got)
	}

	// A later check in the same session must not bring the item back.
	r.Check()
	if lastShown(t, shown) {
		t.Fatal("item shown again after starring in this session")
	}
}

func TestStarRepoStarFailureKeepsItemAndShowsReason(t *testing.T) {
	f := &fakeGH{
		getErr:    errExit1,
		getStderr: notStarred,
		putErr:    errExit1,
		putStderr: []byte("gh: Resource not accessible by integration (HTTP 403)\n"),
	}
	r, shown := newTestStarRepo(f)
	r.Check()

	err := r.Star()
	if err == nil {
		t.Fatal("Star succeeded when gh failed")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("error does not carry gh's reason: %v", err)
	}
	if !lastShown(t, shown) {
		t.Fatal("item hidden after a failed star; it must stay for a retry")
	}
	if n := len(*shown); n != 1 {
		t.Fatalf("visibility changed on failure: %v", *shown)
	}
}

func TestStarRepoHidesOnCheckTimeout(t *testing.T) {
	block := &blockingGH{}
	r := newStarRepo(nil)
	var shown []bool
	r.setShown = func(s bool) { shown = append(shown, s) }
	r.run = block.run
	r.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	r.timeout = 50 * time.Millisecond

	start := time.Now()
	r.Check()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("check ignored its timeout: took %v", elapsed)
	}
	if len(shown) == 0 || shown[len(shown)-1] {
		t.Fatalf("item shown after a timed-out check: %v", shown)
	}
}

// blockingGH is a gh that never answers until its context ends.
type blockingGH struct{}

func (blockingGH) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func TestStarRepoHidesOnUnexpectedAPIError(t *testing.T) {
	f := &fakeGH{getErr: errExit1, getStderr: []byte("gh: Server Error (HTTP 500)\n")}
	r, shown := newTestStarRepo(f)
	r.Check()
	if lastShown(t, shown) {
		t.Fatal("item shown when the starred lookup failed with a server error")
	}
}

func TestStarRepoRechecksOnlyWhenStale(t *testing.T) {
	f := &fakeGH{getErr: errExit1, getStderr: notStarred}
	r, _ := newTestStarRepo(f)
	clock := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }

	r.RecheckIfStale() // never checked: no check yet, so nothing to recheck
	if len(f.calls) != 0 {
		t.Fatalf("recheck ran before any check: %v", f.calls)
	}

	r.Check()
	before := len(f.calls)

	clock = clock.Add(9 * time.Minute)
	r.RecheckIfStale()
	if len(f.calls) != before {
		t.Fatalf("fresh check repeated after 9 minutes: %v", f.calls[before:])
	}

	clock = clock.Add(2 * time.Minute)
	r.RecheckIfStale()
	if len(f.calls) == before {
		t.Fatal("stale check was not repeated after 11 minutes")
	}
}

// TestRunGHUsesGHOnPATH runs the real runner against a fake gh script. It
// covers the exec path and the stderr that the not-starred check reads.
func TestRunGHUsesGHOnPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = api ]; then echo 'gh: Not Found (HTTP 404)' >&2; exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	r := newStarRepo(nil)
	var shown []bool
	r.setShown = func(s bool) { shown = append(shown, s) }
	r.Check()
	if !lastShown(t, &shown) {
		t.Fatal("real runner did not read the 404 as not starred")
	}
}

func TestRunGHMissingFromPATHHidesItem(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := newStarRepo(nil)
	var shown []bool
	r.setShown = func(s bool) { shown = append(shown, s) }
	r.Check()
	if lastShown(t, &shown) {
		t.Fatal("item shown with no gh on PATH")
	}
}
