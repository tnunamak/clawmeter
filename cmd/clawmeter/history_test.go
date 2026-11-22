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

func TestHistoryCommandRejectsInvalidArguments(t *testing.T) {
	bin := buildBinary(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unexpected positional argument", []string{"--json", "extra"}, "Usage: clawmeter history --json"},
		{"invalid duration", []string{"--json", "--since", "not-a-duration"}, `invalid --since duration "not-a-duration"`},
		{"zero duration", []string{"--json", "--since", "0h"}, `invalid --since duration "0h"`},
		{"too many days", []string{"--json", "--since", "36501d"}, `invalid --since duration "36501d"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runWithHome(t, bin, t.TempDir(), append([]string{"history"}, tc.args...)...)
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("history argument error: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

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

func TestHistoryCommandRejectsInvalidV1Record(t *testing.T) {
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
	path := filepath.Join(historyDir, "usage-"+time.Now().UTC().Format("2006-01")+".jsonl")
	if err := os.WriteFile(path, []byte("{\"schema_version\":1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runWithHome(t, bin, home, "history", "--json")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "read history") {
		t.Fatalf("invalid v1 history record: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
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

func TestParseHistoryDurationDaysAndStandardUnits(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"1d": 24 * time.Hour,
		"36500d": 36500 * 24 * time.Hour,
		"90m": 90 * time.Minute,
	} {
		got, err := parseHistoryDuration(input)
		if err != nil || got != want {
			t.Errorf("parseHistoryDuration(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"0d", "-1d", "36501d", "1.5d", "d"} {
		if got, err := parseHistoryDuration(input); err == nil || got != 0 {
			t.Errorf("parseHistoryDuration(%q) = %v, %v; want a zero duration and error", input, got, err)
		}
	}
}

func TestStatusShortcutFlagUsesExactNameBeforeEquals(t *testing.T) {
	for _, arg := range []string{"--source=fixture", "-source=fixture", "--json=false", "-check"} {
		if !isStatusShortcutFlag(arg) {
			t.Errorf("isStatusShortcutFlag(%q) = false; want true", arg)
		}
	}
	for _, arg := range []string{"--source-extra=fixture", "--", "source", "unrelated=--json"} {
		if isStatusShortcutFlag(arg) {
			t.Errorf("isStatusShortcutFlag(%q) = true; want false", arg)
		}
	}
}
