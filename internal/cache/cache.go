// Package cache provides caching for provider usage data.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const defaultTTL = 60 * time.Second

// Entry represents cached usage data for all providers.
type Entry struct {
	// ProviderData maps provider name to their usage data
	ProviderData map[string]*provider.UsageData `json:"provider_data"`
	FetchedAt    time.Time                      `json:"fetched_at"`
}

// cachePath returns the path to the cache file: the platform's user cache
// dir plus clawmeter/usage.json. On Linux this is $XDG_CACHE_HOME (typically
// ~/.cache); on macOS, ~/Library/Caches; on Windows, %LOCALAPPDATA%.
func cachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "usage.json"), nil
}

// Read loads cached usage data from disk.
func Read() (*Entry, error) {
	path, err := cachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// IsValid returns true if the cache entry is fresh (within TTL).
func (e *Entry) IsValid() bool {
	return time.Since(e.FetchedAt) < defaultTTL
}

// Covers reports whether the cache contains an entry — error or data — for
// every name in want. Callers use this in addition to IsValid to avoid
// serving a stale cache that pre-dates a provider becoming configured:
// e.g. the user runs `codex login` after a stale cache was written; without
// this check, status would return empty until the TTL expired.
func (e *Entry) Covers(want []string) bool {
	for _, name := range want {
		if _, ok := e.ProviderData[name]; !ok {
			return false
		}
	}
	return true
}

// GetProvider retrieves usage data for a specific provider.
func (e *Entry) GetProvider(name string) (*provider.UsageData, bool) {
	data, ok := e.ProviderData[name]
	return data, ok
}

// Write saves usage data to the cache.
func Write(result *provider.MultiFetchResult) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	entry := Entry{
		ProviderData: result.Results,
		FetchedAt:    result.FetchedAt,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	path, err := cachePath()
	if err != nil {
		return err
	}

	// Atomic write
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	return os.Rename(tmp, path)
}

func cacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clawmeter"), nil
}
