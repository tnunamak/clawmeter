package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestAppendDedupeChangeAndHourlyHeartbeat(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		at   time.Time
		used float64
	}{{base, 40}, {base.Add(time.Minute), 40}, {base.Add(2 * time.Minute), 41}, {base.Add(63 * time.Minute), 41}} {
		reading := dataAt(item.used, item.at)
		reading.Windows[0].ResetsAt = base.Add(24 * time.Hour)
		if err := Append(dir, map[string]*provider.UsageData{"claude:work": reading}, item.at); err != nil {
			t.Fatal(err)
		}
	}
	if got := countLines(t, dir); got != 3 {
		t.Fatalf("history lines = %d, want 3 (initial, changed, heartbeat)", got)
	}
}

func TestAppendRotatesMonthsAndCapsTotalSize(t *testing.T) {
	dir := t.TempDir()
	oldCap := maxHistoryBytes
	maxHistoryBytes = 650
	t.Cleanup(func() { maxHistoryBytes = oldCap })
	august := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		at := august.Add(time.Duration(i) * time.Hour)
		if err := Append(dir, map[string]*provider.UsageData{"claude:work": dataAt(float64(10+i), at)}, at); err != nil {
			t.Fatal(err)
		}
	}
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := Append(dir, map[string]*provider.UsageData{"claude:work": dataAt(20, september)}, september); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "usage-2026-08.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("old month should be dropped under cap, stat error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			total += info.Size()
		}
	}
	if total > maxHistoryBytes {
		t.Fatalf("history bytes = %d, cap = %d", total, maxHistoryBytes)
	}
}

func TestAppendCapsAnOversizedCurrentMonth(t *testing.T) {
	dir := t.TempDir()
	oldCap := maxHistoryBytes
	maxHistoryBytes = 100
	t.Cleanup(func() { maxHistoryBytes = oldCap })
	at := time.Now().UTC()
	if err := Append(dir, map[string]*provider.UsageData{"claude:work": dataAt(20, at)}, at); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "usage-"+at.Format("2006-01")+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxHistoryBytes {
		t.Fatalf("current month bytes = %d, cap = %d", info.Size(), maxHistoryBytes)
	}
}

func TestHistoryLineHasNoCredentialFieldsAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().UTC()
	if err := Append(dir, map[string]*provider.UsageData{"claude:work": dataAt(20, at)}, at); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "usage-"+at.Format("2006-01")+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	lower := strings.ToLower(line)
	for _, marker := range []string{"token", "secret", "email", "account_id", "credential", "sk-ant-", "eyjhb"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("credential marker %q in history line: %s", marker, line)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatal(err)
	}
	var recorded Reading
	if err := json.Unmarshal([]byte(line), &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.SchemaVersion != SchemaVersion || recorded.Provider != "claude" || recorded.Account != "Work" || recorded.Source != "work" || recorded.Window != "7d Claude 4 Sonnet" || recorded.WindowMinutes != 10080 || recorded.UsedPercent != 20 || !recorded.ResetsAt.Equal(at.Add(24*time.Hour)) || recorded.AgeSeconds != 0 {
		t.Fatalf("history line fields = %#v", recorded)
	}
	for key := range fields {
		if strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "secret") {
			t.Fatalf("credential-like key %q in history", key)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("history permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestReadFiltersProviderAndSince(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		name string
		at   time.Time
	}{{"claude", base}, {"openai", base.Add(time.Hour)}, {"claude", base.Add(2 * time.Hour)}} {
		reading := dataAt(20, item.at)
		reading.Provider = item.name
		if err := Append(dir, map[string]*provider.UsageData{item.name + ":work": reading}, item.at); err != nil {
			t.Fatal(err)
		}
	}
	readings, err := Read(dir, base.Add(time.Minute), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 1 || !readings[0].ReadingTime.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("filtered readings = %#v", readings)
	}
}

func TestAppendInfersWindowMinutesFromName(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().UTC()
	reading := dataAt(20, at)
	reading.Windows[0].Length = 0
	if err := Append(dir, map[string]*provider.UsageData{"claude:work": reading}, at); err != nil {
		t.Fatal(err)
	}
	readings, err := Read(dir, time.Time{}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 1 || readings[0].WindowMinutes != 10080 {
		t.Fatalf("7d window minutes = %#v, want 10080", readings)
	}
}

func dataAt(used float64, at time.Time) *provider.UsageData {
	return &provider.UsageData{Provider: "claude", SourceID: "work", SourceLabel: "Work", FetchedAt: at,
		Windows: []provider.UsageWindow{{Name: "7d Claude 4 Sonnet", Length: 7 * 24 * time.Hour, Utilization: used, ResetsAt: at.Add(24 * time.Hour)}}}
}

func countLines(t *testing.T, dir string) int {
	t.Helper()
	files, err := monthFiles(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	count := 0
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var reading Reading
			if err := json.Unmarshal(scanner.Bytes(), &reading); err != nil || reading.SchemaVersion != SchemaVersion {
				file.Close()
				t.Fatalf("invalid history line: %q, %v", scanner.Bytes(), err)
			}
			count++
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	return count
}
