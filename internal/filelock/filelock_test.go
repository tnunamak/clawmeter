package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// skipWithoutLocks skips where the platform has no file locks.
func skipWithoutLocks(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no file locks on this platform")
	}
}

// A second Acquire waits for the first holder and returns once it releases. The
// lock file stays on disk after release.
func TestAcquireWaitsForTheHolderAndKeepsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, err := Acquire(path, time.Second)
	skipWithoutLocks(t, err)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	go func() {
		second, err := Acquire(path, 10*time.Second)
		if err == nil {
			second()
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("a second Acquire returned while the first lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second Acquire after release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a second Acquire did not return after the first was released")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file was removed: %v", err)
	}
}

// A lock held past the wait gives ErrBusy within about the wait, so a caller can
// stop waiting on a stalled holder.
func TestAcquireGivesUpWhenTheLockStaysBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, err := Acquire(path, time.Second)
	skipWithoutLocks(t, err)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	start := time.Now()
	if _, err := Acquire(path, 50*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("Acquire on a held lock = %v, want ErrBusy", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Acquire waited %v for a 50ms bound", elapsed)
	}
}

// AcquireWatched reports each attempt that finds the lock held. A holder that never
// releases gets at least one report before ErrBusy, so the caller knows it reached the wait.
func TestAcquireWatchedReportsBusyAttempts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, err := Acquire(path, time.Second)
	skipWithoutLocks(t, err)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	busy := 0
	if _, err := AcquireWatched(path, 20*time.Millisecond, func() { busy++ }); !errors.Is(err, ErrBusy) {
		t.Fatalf("AcquireWatched on a held lock = %v, want ErrBusy", err)
	}
	if busy == 0 {
		t.Fatal("no busy attempt was reported")
	}
}

// A free lock is taken without a busy report.
func TestAcquireWatchedStaysQuietOnAFreeLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	busy := 0
	release, err := AcquireWatched(path, time.Second, func() { busy++ })
	skipWithoutLocks(t, err)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if busy != 0 {
		t.Fatalf("a free lock reported %d busy attempts", busy)
	}
}
