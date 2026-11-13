package cli

import (
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// usageCacheWait bounds how long a read waits for another process's fetch to
// reach the cache. A fetch runs under a 30 second context, so one wait normally
// ends with the cache filled. Tests shorten it.
var usageCacheWait = 30 * time.Second

// usageCacheRetry is how often a waiting read checks the cache and the marker.
var usageCacheRetry = 250 * time.Millisecond

// readUsageCache returns the cached usage when it can serve this read. The
// cache serves a read while it is younger than one poll interval and covers
// every configured source at its current revision. Otherwise the read takes the
// fetch marker, so concurrent reads share one fetch rather than each starting
// their own. The caller calls release after writing any fetched data. On a
// cache hit, release does nothing.
func readUsageCache(registry *provider.Registry, cfg *config.Config) (*cache.Entry, func()) {
	noop := func() {}
	deadline := time.Now().Add(usageCacheWait)
	for {
		if entry := servableUsageCache(registry, cfg); entry != nil {
			return entry, noop
		}
		release, ok, err := cache.TakeFetchMarker()
		if err != nil {
			return nil, noop // no marker to share; fetch without one
		}
		if ok {
			// The previous holder may have filled the cache since the check above.
			if entry := servableUsageCache(registry, cfg); entry != nil {
				release()
				return entry, noop
			}
			return nil, release
		}
		if time.Now().After(deadline) {
			return nil, noop // the other fetch is overdue; fetch rather than wait longer
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
	if !entry.CoversCurrent(registry.ConfiguredNames(), sourceRevisions(registry.GetConfigured())) {
		return nil
	}
	return entry
}
