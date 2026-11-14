package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// useUsageCacheTestDir points the usage cache and config at a temporary directory
// and sets the marker wait, restoring the package values when the test ends.
func useUsageCacheTestDir(t *testing.T, wait time.Duration) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)
	savedWait, savedRetry := usageCacheWait, usageCacheRetry
	usageCacheWait, usageCacheRetry = wait, 5*time.Millisecond
	t.Cleanup(func() { usageCacheWait, usageCacheRetry = savedWait, savedRetry })
}

// writeUsageCache writes a cache entry that covers no source, fetched at fetchedAt.
func writeUsageCache(t *testing.T, fetchedAt time.Time) {
	t.Helper()
	if err := cache.Write(&provider.MultiFetchResult{
		Results:         map[string]*provider.UsageData{},
		SourceRevisions: map[string]string{},
		FetchedAt:       fetchedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

// holdUsageMarker holds the fetch marker, as another process's fetch would, until
// the test ends.
func holdUsageMarker(t *testing.T) {
	t.Helper()
	release, ok, err := cache.TakeFetchMarker()
	if err != nil || !ok {
		t.Fatalf("hold marker: ok=%v err=%v", ok, err)
	}
	t.Cleanup(release)
}

// coverageStub is a configured source with no usage, for the coverage tests.
type coverageStub struct{ name string }

func (p coverageStub) Name() string                                            { return p.name }
func (p coverageStub) DisplayName() string                                     { return p.name }
func (p coverageStub) Description() string                                     { return "" }
func (p coverageStub) DashboardURL() string                                    { return "" }
func (p coverageStub) IsConfigured() bool                                      { return true }
func (p coverageStub) FetchUsage(context.Context) (*provider.UsageData, error) { return nil, nil }

// A fresh cache that covers every configured source serves the read, and the
// read takes no fetch marker.
func TestReadUsageCacheServesAFreshEntryWithoutFetching(t *testing.T) {
	useUsageCacheTestDir(t, time.Second)
	writeUsageCache(t, time.Now())
	entry, release, err := readUsageCache(provider.NewRegistry(), &config.Config{})
	if err != nil || entry == nil {
		t.Fatalf("a fresh cache covering nothing configured was not served: entry=%v err=%v", entry, err)
	}
	if held, ok, _ := cache.TakeFetchMarker(); !ok {
		t.Fatal("serving from the cache left the fetch marker held")
	} else {
		held()
	}
	release()
}

// A read that finds the marker held past the wait bound, with no cache to serve,
// returns errFetchInProgress and no entry. The caller then fetches nothing.
func TestReadUsageCacheNeverFetchesPastAnOverdueMarker(t *testing.T) {
	useUsageCacheTestDir(t, 100*time.Millisecond)
	holdUsageMarker(t)
	start := time.Now()
	entry, release, err := readUsageCache(provider.NewRegistry(), &config.Config{})
	if !errors.Is(err, errFetchInProgress) {
		t.Fatalf("err = %v; want errFetchInProgress", err)
	}
	if entry != nil {
		t.Fatal("served an entry from an empty cache")
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("returned after %v, before the wait bound", waited)
	}
	release()
}

// A read that waits out another fetch serves the cache when it covers every
// configured source, even when it is older than a poll interval, because the view
// shows the age.
func TestReadUsageCacheServesAnOverdueCoveringEntry(t *testing.T) {
	useUsageCacheTestDir(t, 100*time.Millisecond)
	writeUsageCache(t, time.Now().Add(-2*time.Hour))
	holdUsageMarker(t)
	entry, release, err := readUsageCache(provider.NewRegistry(), &config.Config{})
	if err != nil || entry == nil {
		t.Fatalf("overdue wait with a covering cache: entry=%v err=%v; want the cache", entry, err)
	}
	release()
}

// An overdue cache that does not cover a configured source is not served. Waiting
// out the fetch cannot give it the source's reading, so the read fails instead.
func TestReadUsageCacheRefusesAnOverdueEntryThatDoesNotCover(t *testing.T) {
	useUsageCacheTestDir(t, 100*time.Millisecond)
	writeUsageCache(t, time.Now().Add(-2*time.Hour))
	holdUsageMarker(t)
	registry := provider.NewRegistry()
	if err := registry.Register(coverageStub{name: "coverage-stub"}); err != nil {
		t.Fatal(err)
	}
	entry, release, err := readUsageCache(registry, &config.Config{})
	if !errors.Is(err, errFetchInProgress) || entry != nil {
		t.Fatalf("entry=%v err=%v; want errFetchInProgress and no entry", entry, err)
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
	entry, release, err := readUsageCache(provider.NewRegistry(), &config.Config{})
	if err != nil {
		t.Fatalf("err = %v; want the marker once the other fetch ends", err)
	}
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

// When no marker can be taken, because the cache directory cannot be created, the
// read returns no entry and no error, so the caller fetches without a marker. It
// does not wait out the wait bound for a marker it cannot have.
func TestReadUsageCacheFetchesWithoutAMarkerWhenNoneCanBeTaken(t *testing.T) {
	useUsageCacheTestDir(t, 5*time.Second)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", blocked)
	t.Setenv("LOCALAPPDATA", blocked)
	t.Setenv("HOME", blocked)
	start := time.Now()
	entry, release, err := readUsageCache(provider.NewRegistry(), &config.Config{})
	if err != nil || entry != nil {
		t.Fatalf("entry=%v err=%v; want no entry and no error, so the caller fetches", entry, err)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("waited %v for a marker that cannot be taken", waited)
	}
	release()
}

// A status read that waits out another process's fetch, with no cache to serve,
// fails with exit code 1 and no output. Before, it fetched on its own.
func TestStatusFailsInsteadOfFetchingPastAnOverdueMarker(t *testing.T) {
	useUsageCacheTestDir(t, 100*time.Millisecond)
	holdUsageMarker(t)
	output, _, code := loadStatusOutput(false)
	if code != 1 || output != nil {
		t.Fatalf("code=%d output-present=%v; want code 1 and no output", code, output != nil)
	}
}
