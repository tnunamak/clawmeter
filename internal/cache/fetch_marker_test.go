package cache

import (
	"os"
	"testing"
	"time"
)

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
	again, ok, err := TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("take after release: ok=%v err=%v; want the marker", ok, err)
	}
	again()
}

// A marker older than FetchMarkerStale belongs to a process that died mid-fetch,
// so the next fetch takes it over.
func TestFetchMarkerTakesOverAStaleMarker(t *testing.T) {
	useFetchMarkerDir(t)
	if _, ok, err := TakeFetchMarker(); err != nil || !ok {
		t.Fatalf("take: ok=%v err=%v; want the marker", ok, err)
	}
	path, err := Path(fetchMarkerName)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * FetchMarkerStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	release, ok, err := TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("take over a marker %v old: ok=%v err=%v; want the marker", 2*FetchMarkerStale, ok, err)
	}
	release()
}
