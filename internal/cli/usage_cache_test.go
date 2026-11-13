package cli

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// useUsageCacheTestDir points the usage cache at a temporary directory and sets
// the marker wait, restoring the package values when the test ends.
func useUsageCacheTestDir(t *testing.T, wait time.Duration) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
	savedWait, savedRetry := usageCacheWait, usageCacheRetry
	usageCacheWait, usageCacheRetry = wait, 5*time.Millisecond
	t.Cleanup(func() { usageCacheWait, usageCacheRetry = savedWait, savedRetry })
}

// A fresh cache that covers every configured source serves the read, and the
// read takes no fetch marker.
func TestReadUsageCacheServesAFreshEntryWithoutFetching(t *testing.T) {
	useUsageCacheTestDir(t, time.Second)
	if err := cache.Write(&provider.MultiFetchResult{
		Results:         map[string]*provider.UsageData{},
		SourceRevisions: map[string]string{},
		FetchedAt:       time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	entry, release := readUsageCache(provider.NewRegistry(), &config.Config{})
	if entry == nil {
		t.Fatal("a fresh cache covering nothing configured was not served")
	}
	if held, ok, _ := cache.TakeFetchMarker(); !ok {
		t.Fatal("serving from the cache left the fetch marker held")
	} else {
		held()
	}
	release()
}

// A read that finds the marker held by a fetch that never finishes stops
// waiting after the wait bound, and then fetches on its own.
func TestReadUsageCacheStopsWaitingForAnOverdueFetch(t *testing.T) {
	useUsageCacheTestDir(t, 100*time.Millisecond)
	holder, ok, err := cache.TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("hold marker: ok=%v err=%v", ok, err)
	}
	defer holder()
	start := time.Now()
	entry, release := readUsageCache(provider.NewRegistry(), &config.Config{})
	if entry != nil {
		t.Fatal("served an entry from an empty cache")
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("returned after %v, before the wait bound", waited)
	}
	release()
}

// A read that takes the marker holds it until release, so other reads wait on
// this fetch instead of starting another.
func TestReadUsageCacheHoldsTheMarkerItTakes(t *testing.T) {
	useUsageCacheTestDir(t, time.Second)
	holder, ok, err := cache.TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("hold marker: ok=%v err=%v", ok, err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		holder()
	}()
	entry, release := readUsageCache(provider.NewRegistry(), &config.Config{})
	if entry != nil {
		t.Fatal("served an entry from an empty cache")
	}
	if _, ok, _ := cache.TakeFetchMarker(); ok {
		t.Fatal("the read that fetches did not hold the marker")
	}
	release()
	if again, ok, _ := cache.TakeFetchMarker(); !ok {
		t.Fatal("release did not free the marker")
	} else {
		again()
	}
}
