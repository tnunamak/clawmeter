package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestHistoryRecoversAfterInterruptedProcess(t *testing.T) {
	for _, mode := range []string{"torn-tail", "complete-tail", "flush-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
			if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(20, at)}, at); err != nil {
				t.Fatal(err)
			}
			child := startHistoryProcess(t, dir, mode)
			child.expect(t, "holding")
			if err := child.cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := child.cmd.Wait(); err == nil {
				t.Fatal("interrupted writer exited successfully")
			}
			fresh := startHistoryProcess(t, dir, "append", "CLAWMETER_HISTORY_USED=22")
			fresh.expect(t, "acquired")
			fresh.wait(t)
			readings, err := Read(dir, time.Time{}, "")
			want := 2
			if mode == "complete-tail" {
				want = 3
			}
			if err != nil || len(readings) != want || readings[0].UsedPercent != 20 || readings[len(readings)-1].UsedPercent != 22 {
				t.Fatalf("recovered process history: %#v, %v; want %d records", readings, err, want)
			}
			assertHistorySizeAndLines(t, dir)
		})
	}
}

// Fault injection writes real bytes while holding the production lock, then
// leaves the child at a known pre-newline boundary for the parent to kill.
func writeInterruptedTail(t *testing.T, dir, mode string) {
	t.Helper()
	path := filepath.Join(dir, "usage-2026-10.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reading Reading
	if err := json.Unmarshal(data, &reading); err != nil {
		t.Fatal(err)
	}
	reading.UsedPercent = 21
	line, err := json.Marshal(reading)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if mode == "flush-error" {
		writer := bufio.NewWriter(&partialFlushWriter{file: file, remaining: len(line) / 2})
		if _, err := writer.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := writer.Flush(); !errors.Is(err, errInjectedFlush) {
			t.Fatalf("flush error = %v", err)
		}
	} else {
		if mode == "torn-tail" {
			line = line[:len(line)/2]
		}
		if _, err := file.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

var errInjectedFlush = errors.New("injected partial flush failure")

type partialFlushWriter struct {
	file      *os.File
	remaining int
}

func (writer *partialFlushWriter) Write(data []byte) (int, error) {
	n, err := writer.file.Write(data[:min(len(data), writer.remaining)])
	writer.remaining -= n
	if err == nil {
		err = errInjectedFlush
	}
	return n, err
}

func TestHistoryRecoversUnterminatedTail(t *testing.T) {
	base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	for _, tail := range []string{"torn", "complete"} {
		for _, nextMonth := range []bool{false, true} {
			name := tail + "/same-month"
			if nextMonth {
				name = tail + "/next-month"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "usage-2026-10.jsonl")
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(20, base)}, base); err != nil {
					t.Fatal(err)
				}
				prefix, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				ending := []byte(`{"schema_version":1,"reading_time":`)
				wantBefore := 1
				if tail == "complete" {
					ending = bytes.TrimSuffix(prefix, []byte{'\n'})
					wantBefore = 2
				}
				if err := os.WriteFile(path, append(bytes.Clone(prefix), ending...), 0600); err != nil {
					t.Fatal(err)
				}
				readings, err := Read(dir, time.Time{}, "")
				if err != nil || len(readings) != wantBefore {
					t.Fatalf("read before recovery: %d records, %v; want %d records", len(readings), err, wantBefore)
				}
				at := base.Add(time.Minute)
				if nextMonth {
					at = base.AddDate(0, 1, 0)
				}
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(21, at)}, at); err != nil {
					t.Fatal(err)
				}
				readings, err = Read(dir, time.Time{}, "")
				if err != nil || len(readings) != wantBefore+1 {
					t.Fatalf("read after recovery: %d records, %v", len(readings), err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.HasPrefix(after, prefix) || !bytes.HasSuffix(after, []byte{'\n'}) {
					t.Fatalf("recovery changed committed prefix or omitted newline: %q, %v", after, err)
				}
				assertHistorySizeAndLines(t, dir)
			})
		}
	}
}

func TestHistoryRejectsInteriorAndTerminatedCorruption(t *testing.T) {
	for _, suffix := range []string{"\n", "\n{\"schema_version\":1}"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "usage-2026-10.jsonl")
			before := []byte("broken" + suffix)
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(dir, time.Time{}, ""); err == nil {
				t.Fatal("Read accepted corruption")
			}
			if err := Append(dir, nil, time.Now()); err == nil {
				t.Fatal("Append accepted corruption")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("corrupt file changed: %q, %v", after, err)
			}
		})
	}
}

func TestAppendCapsHistoryAfterMonthRollback(t *testing.T) {
	oldCap := maxHistoryBytes
	t.Cleanup(func() { maxHistoryBytes = oldCap })
	for _, limit := range []int64{650, 100} {
		t.Run(fmt.Sprintf("cap=%d", limit), func(t *testing.T) {
			dir := t.TempDir()
			maxHistoryBytes = 1 << 20
			for _, month := range []time.Month{time.November, time.December} {
				at := time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC)
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(20, at)}, at); err != nil {
					t.Fatal(err)
				}
			}
			maxHistoryBytes = limit
			base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 4; i++ {
				at := base.Add(time.Duration(i) * time.Hour)
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(float64(21+i), at)}, at); err != nil {
					t.Fatal(err)
				}
				assertHistorySizeAndLines(t, dir)
			}
		})
	}
}

func TestAppendHeartbeatAfterClockRollback(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	for _, minutes := range []int{31 * 24 * 60, 0, 1, 59, 60, 61, 120} {
		at := base.Add(time.Duration(minutes) * time.Minute)
		data := dataAt(20, at)
		data.Windows[0].ResetsAt = base.Add(60 * 24 * time.Hour)
		if err := Append(dir, map[string]*provider.UsageData{"work": data}, at); err != nil {
			t.Fatal(err)
		}
	}
	readings, err := Read(dir, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour), base.Add(31 * 24 * time.Hour)}
	if len(readings) != len(want) {
		t.Fatalf("heartbeat records = %d, want %d: %#v", len(readings), len(want), readings)
	}
	for i, at := range want {
		if !readings[i].RecordedAt.Equal(at) {
			t.Fatalf("record %d at %s, want %s", i, readings[i].RecordedAt, at)
		}
	}
	changed := dataAt(21, base.Add(121*time.Minute))
	changed.Windows[0].ResetsAt = base.Add(60 * 24 * time.Hour)
	if err := Append(dir, map[string]*provider.UsageData{"work": changed}, changed.FetchedAt); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, dir); got != 5 {
		t.Fatalf("changed usage after rollback: %d records, want 5", got)
	}
}

func TestHistoryRejectsUnsupportedVersionsWithoutMutation(t *testing.T) {
	for _, version := range []string{"0", "2", "missing"} {
		for _, terminated := range []bool{false, true} {
			t.Run(fmt.Sprintf("version=%s/newline=%t", version, terminated), func(t *testing.T) {
				dir := t.TempDir()
				base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(20, base)}, base); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "usage-2026-10.jsonl")
				prefix, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				line := bytes.Replace(prefix, []byte(`"schema_version":1,`), []byte(`"schema_version":`+version+`,`), 1)
				if version == "missing" {
					line = bytes.Replace(prefix, []byte(`"schema_version":1,`), nil, 1)
				}
				if !terminated {
					line = bytes.TrimSuffix(line, []byte{'\n'})
				}
				before := append(bytes.Clone(prefix), line...)
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := Read(dir, base.Add(time.Hour), "other"); err == nil || !strings.Contains(err.Error(), "unsupported history schema version") {
					t.Fatalf("Read version error = %v", err)
				}
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(21, base)}, base); err == nil || !strings.Contains(err.Error(), "unsupported history schema version") {
					t.Errorf("Append version error = %v", err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Errorf("unsupported history changed: %q, %v", after, err)
				}
			})
		}
	}
}

func assertHistorySizeAndLines(t *testing.T, dir string) {
	t.Helper()
	files, err := monthFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		total += int64(len(data))
		if len(data) > 0 && data[len(data)-1] != '\n' {
			t.Fatalf("unterminated history: %s", path)
		}
		for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
			if len(data) == 0 {
				continue
			}
			var reading Reading
			if err := json.Unmarshal(line, &reading); err != nil || reading.SchemaVersion != SchemaVersion {
				t.Fatalf("invalid retained line: %q, %v", line, err)
			}
		}
	}
	if total > maxHistoryBytes {
		t.Fatalf("history bytes = %d, cap = %d", total, maxHistoryBytes)
	}
}
