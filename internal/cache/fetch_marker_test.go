package cache

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// fetchMarkerHolderEnv makes TestFetchMarkerHolderProcess run as the holder in
// TestFetchMarkerIsReleasedWhenItsHolderDies. Without it the helper is skipped.
const fetchMarkerHolderEnv = "CLAWMETER_FETCH_MARKER_HOLDER"

// useFetchMarkerDir points the cache at a temporary directory for one test.
func useFetchMarkerDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
}

func TestFetchMarkerAdmitsOneHolderAtATime(t *testing.T) {
	useFetchMarkerDir(t)
	release, ok, err := TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("first take: ok=%v err=%v; want the marker", ok, err)
	}
	if _, ok, err := TakeFetchMarker(); err != nil || ok {
		t.Fatalf("second take while held: ok=%v err=%v; want no marker", ok, err)
	}
	release()
	path, err := Path(fetchMarkerName)
	if err != nil {
		t.Fatal(err)
	}
	// Removing the file on release would let a second process lock a new file at
	// the same path while this one still holds the old file.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("release removed the lock file: %v", err)
	}
	again, ok, err := TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("take after release: ok=%v err=%v; want the marker", ok, err)
	}
	again()
}

// A held marker is not taken over by age. Its holder is a live process, and a
// fetch can run longer than any fixed age, so the claim lasts until the lock is
// released.
func TestFetchMarkerIsNotTakenOverByAge(t *testing.T) {
	useFetchMarkerDir(t)
	release, ok, err := TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("take: ok=%v err=%v; want the marker", ok, err)
	}
	defer release()
	path, err := Path(fetchMarkerName)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := TakeFetchMarker(); err != nil || ok {
		t.Fatalf("held marker taken over once ten minutes old: ok=%v err=%v; want no marker", ok, err)
	}
}

// A marker held by a process that dies is free at once. The kernel drops a
// process's file locks when it exits, so the test kills the holder with no
// cleanup on its side, as a crash would, and expects the marker to be free.
func TestFetchMarkerIsReleasedWhenItsHolderDies(t *testing.T) {
	useFetchMarkerDir(t)
	holder := exec.Command(os.Args[0], "-test.run=^TestFetchMarkerHolderProcess$")
	holder.Env = append(os.Environ(), fetchMarkerHolderEnv+"=1")
	// The holder exits when its stdin closes, so it cannot outlive this test.
	if _, err := holder.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	stop := func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	}
	reported, _ := bufio.NewReader(stdout).ReadString('\n')
	if reported != "held\n" {
		stop()
		t.Fatalf("holder reported %q; want it to hold the marker", reported)
	}
	if _, ok, err := TakeFetchMarker(); err != nil || ok {
		stop()
		t.Fatalf("marker taken while its holder process runs: ok=%v err=%v; want no marker", ok, err)
	}
	stop()
	release, ok, err := TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("marker not free after its holder died: ok=%v err=%v; want the marker", ok, err)
	}
	release()
}

// TestFetchMarkerHolderProcess is the holder that
// TestFetchMarkerIsReleasedWhenItsHolderDies starts in another process. It takes
// the marker, reports that it holds it, and runs until its stdin closes.
func TestFetchMarkerHolderProcess(t *testing.T) {
	if os.Getenv(fetchMarkerHolderEnv) != "1" {
		t.Skip("runs only as the holder process of TestFetchMarkerIsReleasedWhenItsHolderDies")
	}
	if _, ok, err := TakeFetchMarker(); err != nil || !ok {
		t.Fatalf("holder: ok=%v err=%v; want the marker", ok, err)
	}
	fmt.Println("held")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
