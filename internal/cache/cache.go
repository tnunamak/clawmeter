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

// cachePath returns the path to the cache file.
func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "clawmeter", "usage.json"), nil
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "clawmeter"), nil
}
