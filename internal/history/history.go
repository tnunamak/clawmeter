// Package history stores a bounded, local JSONL history of successful quota readings.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	SchemaVersion = 1
	MaxBytes      = 50 << 20
	lockTimeout   = 5 * time.Second
)

var maxHistoryBytes int64 = MaxBytes

// Reading is the stable line format written to the local history.
type Reading struct {
	SchemaVersion int       `json:"schema_version"`
	ReadingTime   time.Time `json:"reading_time"`
	RecordedAt    time.Time `json:"recorded_at"`
	Provider      string    `json:"provider"`
	Account       string    `json:"account"`
	Source        string    `json:"source"`
	Window        string    `json:"window"`
	WindowMinutes int       `json:"window_minutes"`
	UsedPercent   float64   `json:"used_percent"`
	ResetsAt      time.Time `json:"resets_at"`
	AgeSeconds    int64     `json:"age_seconds"`
}

// Append records changed windows and writes an hourly heartbeat for unchanged
// readings. dir is the history directory, which keeps storage policy testable.
func Append(dir string, results map[string]*provider.UsageData, now time.Time) error {
	lock, err := acquireLock(dir)
	if err != nil {
		return err
	}
	defer lock.Close()

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("protect history directory: %w", err)
	}
	last, tails, err := latestByKey(dir, now)
	if err != nil {
		return err
	}
	// Validate every file before repairing tails, so unsupported versions or
	// interior corruption leave all history bytes untouched.
	for _, tail := range tails {
		if err := tail.repair(); err != nil {
			return err
		}
	}
	path := filepath.Join(dir, "usage-"+now.UTC().Format("2006-01")+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return fmt.Errorf("protect history file: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, data := range results {
		if data == nil || data.Error != "" || data.IsExpired || data.Stale || data.FetchedAt.IsZero() {
			continue
		}
		providerName := safeValue(data.Provider)
		if providerName == "" {
			continue
		}
		source := safeValue(data.SourceID)
		account := safeValue(data.SourceLabel)
		if source == "" {
			source = "default"
		}
		if account == "" {
			account = source
		}
		for _, window := range data.Windows {
			windowName := safeValue(window.Name)
			length := window.Length
			if length == 0 && !window.LengthUnknown {
				length = provider.WindowLengthFromName(window.Name)
			}
			if windowName == "" || length < 0 || window.Utilization < 0 || math.IsNaN(window.Utilization) || math.IsInf(window.Utilization, 0) {
				continue
			}
			reading := Reading{
				SchemaVersion: SchemaVersion,
				ReadingTime:   data.FetchedAt.UTC(), RecordedAt: now.UTC(),
				Provider: providerName, Account: account, Source: source,
				Window: windowName, WindowMinutes: int(length.Minutes()),
				UsedPercent: window.Utilization, ResetsAt: window.ResetsAt.UTC(),
				AgeSeconds: int64(max(0, now.Sub(data.FetchedAt).Seconds())),
			}
			key := historyKey(reading)
			if previous, ok := last[key]; ok && sameUsage(previous, reading) && now.Sub(previous.RecordedAt) < time.Hour {
				continue
			}
			line, err := json.Marshal(reading)
			if err != nil {
				file.Close()
				return fmt.Errorf("encode history reading: %w", err)
			}
			if _, err := writer.Write(append(line, '\n')); err != nil {
				file.Close()
				return fmt.Errorf("write history reading: %w", err)
			}
			last[key] = reading
		}
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return fmt.Errorf("flush history: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync history: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close history: %w", err)
	}
	return enforceCap(dir, path)
}

// Read returns records in reading-time order. An empty provider means all
// providers; a zero since value means no lower time bound. A malformed final
// record without a newline is ignored; Append repairs that tail under the lock.
func Read(dir string, since time.Time, providerName string) ([]Reading, error) {
	lock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	var readings []Reading
	_, err = scanHistory(dir, func(reading Reading) {
		if !reading.ReadingTime.Before(since) && (providerName == "" || reading.Provider == providerName) {
			readings = append(readings, reading)
		}
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(readings, func(i, j int) bool {
		return readings[i].ReadingTime.Before(readings[j].ReadingTime)
	})
	return readings, nil
}

func latestByKey(dir string, now time.Time) (map[string]Reading, []historyTail, error) {
	latest := make(map[string]Reading)
	tails, err := scanHistory(dir, func(reading Reading) {
		// Ignore future anchors after a clock rollback. The next observation
		// establishes a new anchor, so later polls still dedupe normally.
		if reading.RecordedAt.After(now) {
			return
		}
		key := historyKey(reading)
		if previous, ok := latest[key]; !ok || !reading.RecordedAt.Before(previous.RecordedAt) {
			latest[key] = reading
		}
	})
	return latest, tails, err
}

// scanHistory shares the version and tail policy between Read and Append.
// It never mutates files; repairs are applied only after all files validate.
func scanHistory(dir string, visit func(Reading)) ([]historyTail, error) {
	files, err := monthFiles(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tails []historyTail
	for _, path := range files {
		tail, err := scanHistoryFile(path, visit)
		if err != nil {
			return nil, err
		}
		if tail != nil {
			tails = append(tails, *tail)
		}
	}
	return tails, nil
}

type historyTail struct {
	path    string
	offset  int64
	newline bool
}

func scanHistoryFile(path string, visit func(Reading)) (tail *historyTail, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open history file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	// Keep delimiters so only an unterminated final record can be recovered.
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var offset int64
	for scanner.Scan() {
		line := scanner.Bytes()
		terminated := line[len(line)-1] == '\n'
		if !terminated && !json.Valid(line) {
			tail = &historyTail{path: path, offset: offset}
			continue
		}
		var reading Reading
		if err := json.Unmarshal(line, &reading); err != nil {
			return nil, fmt.Errorf("decode history line in %s: %w", filepath.Base(path), err)
		}
		if reading.SchemaVersion != SchemaVersion {
			return nil, fmt.Errorf("unsupported history schema version %d", reading.SchemaVersion)
		}
		visit(reading)
		offset += int64(len(line))
		if !terminated {
			tail = &historyTail{path: path, offset: offset, newline: true}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read history file: %w", err)
	}
	return tail, nil
}

// repair preserves complete records, adds a missing delimiter, or removes only
// malformed bytes after the final delimiter. The caller holds the history lock.
func (tail historyTail) repair() error {
	file, err := os.OpenFile(tail.path, os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open history tail: %w", err)
	}
	defer file.Close()
	if tail.newline {
		_, err = file.WriteAt([]byte{'\n'}, tail.offset)
	} else {
		err = file.Truncate(tail.offset)
	}
	if err != nil {
		return fmt.Errorf("repair history tail: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync history tail: %w", err)
	}
	return nil
}

func monthFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "usage-") && strings.HasSuffix(name, ".jsonl") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

func enforceCap(dir, current string) error {
	files, err := monthFiles(dir)
	if err != nil {
		return err
	}
	type sizedFile struct {
		path string
		size int64
	}
	var total int64
	var sizes []sizedFile
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		total += info.Size()
		sizes = append(sizes, sizedFile{path: path, size: info.Size()})
	}
	for _, file := range sizes {
		if total <= maxHistoryBytes {
			break
		}
		if file.path == current {
			continue
		}
		if err := os.Remove(file.path); err != nil {
			return fmt.Errorf("rotate old history month: %w", err)
		}
		total -= file.size
	}
	if total > maxHistoryBytes {
		return trimOldestLines(current, maxHistoryBytes)
	}
	return nil
}

func trimOldestLines(path string, limit int64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if int64(len(data)) <= limit {
		return nil
	}
	start := len(data) - int(limit)
	if newline := bytes.IndexByte(data[start:], '\n'); newline >= 0 {
		start += newline + 1
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data[start:]); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func historyKey(reading Reading) string {
	return reading.Provider + "\x00" + reading.Source + "\x00" + reading.Window
}

func sameUsage(a, b Reading) bool {
	return a.Account == b.Account && a.Provider == b.Provider && a.Source == b.Source &&
		a.Window == b.Window && a.WindowMinutes == b.WindowMinutes &&
		a.UsedPercent == b.UsedPercent && a.ResetsAt.Equal(b.ResetsAt)
}

func safeValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n\x00") || len(value) > 80 {
		return ""
	}
	if strings.ContainsAny(value, "@/\\:") {
		return ""
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"akia", "asia", "aiza", "eyj", "ghp_", "github_pat_", "glpat-", "sk-", "sk_", "sk-ant-", "xoxb-", "xoxp-", "xoxa-", "xoxr-", "ya29."} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	compact := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
	if len(compact) >= 24 {
		letters, digits := false, false
		for _, r := range compact {
			letters = letters || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			digits = digits || r >= '0' && r <= '9'
		}
		if letters && digits {
			return ""
		}
	}
	return value
}
