package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tnunamak/clawmeter/internal/api"
)

const defaultTTL = 60 * time.Second

type Entry struct {
	Usage     *api.UsageResponse `json:"usage"`
	FetchedAt time.Time          `json:"fetched_at"`
}

func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "clawmeter"), nil
}

func cachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "usage.json"), nil
}

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

func (e *Entry) IsValid() bool {
	return time.Since(e.FetchedAt) < defaultTTL
}

func Write(usage *api.UsageResponse) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	entry := Entry{
		Usage:     usage,
		FetchedAt: time.Now(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	path, err := cachePath()
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	return os.Rename(tmp, path)
}
