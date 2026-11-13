package cli

import (
	"errors"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// usageCacheWait bounds how long a read waits behind another process's fetch.
// That fetch runs under a 30-second context and writes the cache right after, so
// a wait of this length normally ends with the cache filled. Tests shorten it.
var usageCacheWait = 30 * time.Second

// usageCacheRetry is how often a waiting read checks the cache and the marker.
var usageCacheRetry = 250 * time.Millisecond

// errFetchInProgress means another process is still fetching usage, and the cache
// cannot serve this read.
var errFetchInProgress = errors.New("another process is still fetching usage")

// readUsageCache returns the cached usage when it can serve this read. The cache
// serves while it is younger than one poll interval and covers every configured
// source at its current revision. Otherwise the read takes the fetch marker, so
// concurrent reads share one fetch. It then returns a nil entry and the marker's
// release, which the caller calls after writing the fetched data. A cache hit
// returns a release that does nothing.
//
// A read that waits usageCacheWait behind another process's fetch serves the
// cache if it covers every configured source, at any age, because the view shows
// the age. Otherwise it returns errFetchInProgress. It never fetches while another
// process holds the marker. If no marker can be taken at all, because the cache
// directory cannot be created or the platform has no file locks, it returns a nil
// entry and no error, and the caller fetches without one: nothing can coordinate
// that fetch.
func readUsageCache(registry *provider.Registry, cfg *config.Config) (*cache.Entry, func(), error) {
	noop := func() {}
	deadline := time.Now().Add(usageCacheWait)
	for {
		if entry := servableUsageCache(registry, cfg); entry != nil {
			return entry, noop, nil
		}
		release, ok, err := cache.TakeFetchMarker()
		if err != nil {
			return nil, noop, nil
		}
		if ok {
			// The previous holder may have filled the cache since the check above.
			if entry := servableUsageCache(registry, cfg); entry != nil {
				release()
				return entry, noop, nil
			}
			return nil, release, nil
		}
		if time.Now().After(deadline) {
			if entry := overdueUsageCache(registry); entry != nil {
				return entry, noop, nil
			}
			return nil, noop, errFetchInProgress
		}
		time.Sleep(usageCacheRetry)
	}
}

// servableUsageCache returns the cache entry when a read may serve from it, or
// nil. The poll interval is the freshness bound, so a served entry is never older
// than the tray would have fetched it.
func servableUsageCache(registry *provider.Registry, cfg *config.Config) *cache.Entry {
	entry, err := cache.Read()
	if err != nil || !entry.IsFresh(config.PollInterval(cfg.Settings.PollInterval)) {
		return nil
	}
	if !coversConfigured(entry, registry) {
		return nil
	}
	return entry
}

// overdueUsageCache returns the cache entry, whatever its age, when it covers
// every configured source, or nil.
func overdueUsageCache(registry *provider.Registry) *cache.Entry {
	entry, err := cache.Read()
	if err != nil || !coversConfigured(entry, registry) {
		return nil
	}
	return entry
}

// coversConfigured reports whether entry covers every configured source at its
// current revision.
func coversConfigured(entry *cache.Entry, registry *provider.Registry) bool {
	return entry.CoversCurrent(registry.ConfiguredNames(), sourceRevisions(registry.GetConfigured()))
}
