package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHistoryCommandJSONLinesFilters(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".cache")
	if runtime.GOOS == "windows" {
		cacheRoot = filepath.Join(home, "AppData", "Local")
	}
	historyDir := filepath.Join(cacheRoot, "clawmeter", "history")
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lines := []any{
		map[string]any{"schema_version": 1, "reading_time": now.Add(-time.Hour), "recorded_at": now.Add(-time.Hour), "provider": "claude", "account": "Work", "source": "work", "window": "7d", "window_minutes": 10080, "used_percent": 40, "resets_at": now.Add(time.Hour), "age_seconds": 0},
		map[string]any{"schema_version": 1, "reading_time": now.Add(-2 * time.Hour), "recorded_at": now.Add(-2 * time.Hour), "provider": "openai", "account": "default", "source": "default", "window": "7d", "window_minutes": 10080, "used_percent": 20, "resets_at": now.Add(time.Hour), "age_seconds": 0},
	}
	path := filepath.Join(historyDir, "usage-"+now.Format("2006-01")+".jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, line := range lines {
		if err := encoder.Encode(line); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runWithHome(t, bin, home, "history", "--json", "--since", "7d", "--provider", "claude")
	if code != 0 || stderr != "" || strings.Count(strings.TrimSpace(stdout), "\n") != 0 || !strings.Contains(stdout, `"provider":"claude"`) {
		t.Fatalf("history command: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, stderr, code := runWithHome(t, bin, home, "history"); code == 0 || !strings.Contains(stderr, "--json") {
		t.Fatalf("history without --json: exit=%d stderr=%q", code, stderr)
	}
}

func TestUsageHistoryConfigSetting(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	stdout, stderr, code := runWithHome(t, bin, home, "config", "show")
	if code != 0 || !strings.Contains(stdout, "Usage history: true") {
		t.Fatalf("default history setting: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code = runWithHome(t, bin, home, "config", "set", "usage_history", "false"); code != 0 {
		t.Fatalf("disable history: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runWithHome(t, bin, home, "config", "show")
	if code != 0 || !strings.Contains(stdout, "Usage history: false") {
		t.Fatalf("show disabled history: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
