package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	localhistory "github.com/tnunamak/clawmeter/internal/history"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestHistoryOptOutsSkipWrites(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("LOCALAPPDATA", cacheHome)
	t.Setenv("HOME", cacheHome)
	reading := &provider.UsageData{Provider: "claude", SourceID: "work", SourceLabel: "Work", FetchedAt: time.Now().UTC(),
		Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 25, ResetsAt: time.Now().Add(24 * time.Hour)}}}
	results := map[string]*provider.UsageData{"claude:work": reading}
	cfg := config.DefaultConfig()
	dir, err := cache.Path("history")
	if err != nil {
		t.Fatal(err)
	}
	recordUsageHistory(results, cfg, true)
	disabled := false
	cfg.Settings.UsageHistory = &disabled
	recordUsageHistory(results, cfg)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("opted-out history directory stat error = %v, want not exist", err)
	}
	cfg.Settings.UsageHistory = nil
	recordUsageHistory(results, cfg)
	readings, err := localhistory.Read(dir, time.Time{}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 1 {
		t.Fatalf("enabled history records = %d, want 1", len(readings))
	}
	if _, err := os.Stat(filepath.Join(dir, ".usage-history.lock")); err != nil {
		t.Fatalf("history lock was not created: %v", err)
	}
}
