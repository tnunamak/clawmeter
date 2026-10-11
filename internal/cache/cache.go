// Package cache provides caching for provider usage data.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const defaultTTL = 60 * time.Second

// Allow one missed refresh before cached-only output is marked stale.
const staleMargin = time.Minute

// schemaVersion invalidates caches whose values changed meaning. Version 2:
// Claude extra-usage Used/Limit were written 100x too large before; entries
// without this version are dropped on read, including stale fallbacks.
const schemaVersion = 2

var writeMu sync.Mutex

// Entry represents cached usage data for all providers.
type Entry struct {
	Version int `json:"version,omitempty"`
	// ProviderData maps canonical source key to usage data. The legacy `claude`
	// key remains the default Claude source.
	ProviderData    map[string]*provider.UsageData `json:"provider_data"`
	SourceRevisions map[string]string              `json:"source_revisions,omitempty"`
	FetchedAt       time.Time                      `json:"fetched_at"`
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
	if entry.Version != schemaVersion {
		return nil, fmt.Errorf("cache schema version %d is not %d", entry.Version, schemaVersion)
	}
	return &entry, nil
}

// IsValid returns true if the cache entry is fresh (within TTL).
func (e *Entry) IsValid() bool {
	return time.Since(e.FetchedAt) < defaultTTL
}

// IsStale reports when cached-only output must warn about its age.
func (e *Entry) IsStale() bool {
	return time.Since(e.FetchedAt) >= defaultTTL+staleMargin
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

func (e *Entry) CoversCurrent(want []string, revisions map[string]string) bool {
	if !e.Covers(want) {
		return false
	}
	for _, name := range want {
		if !SourceRevisionMatches(e.SourceRevisions, name, revisions[name]) {
			return false
		}
	}
	return true
}

// SourceRevisionMatches compares provenance symmetrically. An unrevisioned
// source matches only an unrevisioned cache entry; this prevents a source key
// that changes between native and explicit credentials from reusing old data.
func SourceRevisionMatches(cached map[string]string, name, current string) bool {
	previous, hadPrevious := cached[name]
	if previous == "" {
		hadPrevious = false
	}
	hasCurrent := current != ""
	return hadPrevious == hasCurrent && (!hasCurrent || previous == current)
}

// HasStaleData reports whether any requested provider is cached fallback data.
func (e *Entry) HasStaleData(want []string) bool {
	for _, name := range want {
		if data := e.ProviderData[name]; data != nil && data.Stale {
			return true
		}
	}
	return false
}

// GetProvider retrieves usage data for a specific provider.
func (e *Entry) GetProvider(name string) (*provider.UsageData, bool) {
	data, ok := e.ProviderData[name]
	return data, ok
}

// Write saves usage data to the cache.
func Write(result *provider.MultiFetchResult) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	return writeEntry(Entry{
		ProviderData: result.Results, SourceRevisions: result.SourceRevisions,
		FetchedAt: result.FetchedAt,
	})
}

// UpdateProvider merges one locally observed provider source without extending
// the freshness lifetime of the other providers in the cache.
func UpdateProvider(name string, data *provider.UsageData, sourceRevision string) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	entry, err := Read()
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		entry = &Entry{ProviderData: make(map[string]*provider.UsageData)}
	}
	if entry.ProviderData == nil {
		entry.ProviderData = make(map[string]*provider.UsageData)
	}
	entry.ProviderData[name] = data
	if entry.SourceRevisions == nil {
		entry.SourceRevisions = make(map[string]string)
	}
	if sourceRevision == "" {
		delete(entry.SourceRevisions, name)
	} else {
		entry.SourceRevisions[name] = sourceRevision
	}
	return writeEntry(*entry)
}

func writeEntry(entry Entry) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	entry.Version = schemaVersion
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	path, err := cachePath()
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".usage-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0600); err != nil {
		return fmt.Errorf("protect temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}

func cacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clawmeter"), nil
}
