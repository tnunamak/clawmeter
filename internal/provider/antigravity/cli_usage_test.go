package antigravity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCLIUsageReadsStructuredUsageCommand(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fake executable requires Unix")
	}
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "agy")
	report := `{"status":"SUCCESS","num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"command":{"name":"usage","data":{"groups":[{"name":"New Pool","description":"Group description","buckets":[{"id":"new-weekly","name":"Limit","description":"Bucket description","window":"weekly","remaining_fraction":0,"reset_time":"2026-11-01T03:48:22Z"}]}]}}}`
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + argsPath + "\"\nprintf '%s' '" + report + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.lookPath = func(string) (string, error) { return bin, nil }
	p.now = func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(args)), "--print /usage --output-format json --print-timeout 20s"; got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if data.Provider != "antigravity" || len(data.Windows) != 1 || data.Windows[0].Utilization != 100 {
		t.Fatalf("unexpected usage data: %#v", data)
	}
}

func TestCLIUsageRejectsInvalidReports(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fake executable requires Unix")
	}
	valid := `{"status":"SUCCESS","num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"command":{"name":"usage","data":{"groups":[{"name":"G","buckets":[{"id":"x","window":"weekly","remaining_fraction":0}]}]}}}`
	for name, report := range map[string]string{
		"missing remaining":       strings.Replace(valid, `,"remaining_fraction":0`, "", 1),
		"null remaining":          strings.Replace(valid, `"remaining_fraction":0`, `"remaining_fraction":null`, 1),
		"invalid remaining":       strings.Replace(valid, `"remaining_fraction":0`, `"remaining_fraction":1.2`, 1),
		"nonzero turns":           strings.Replace(valid, `"num_turns":0`, `"num_turns":1`, 1),
		"missing turns":           strings.Replace(valid, `"num_turns":0,`, "", 1),
		"null turns":              strings.Replace(valid, `"num_turns":0`, `"num_turns":null`, 1),
		"nonzero tokens":          strings.Replace(valid, `"total_tokens":0`, `"total_tokens":1`, 1),
		"null tokens":             strings.Replace(valid, `"total_tokens":0`, `"total_tokens":null`, 1),
		"missing tokens":          strings.Replace(valid, `,"total_tokens":0`, "", 1),
		"thinking tokens":         strings.Replace(valid, `"total_tokens":0`, `"total_tokens":0,"thinking_tokens":1`, 1),
		"future activity counter": strings.Replace(valid, `"total_tokens":0`, `"total_tokens":0,"future_tokens":1`, 1),
		"wrong command":           strings.Replace(valid, `"name":"usage"`, `"name":"models"`, 1),
		"unsuccessful command":    strings.Replace(valid, `"status":"SUCCESS"`, `"status":"ERROR"`, 1),
		"disabled only":           strings.Replace(valid, `"remaining_fraction":0`, `"remaining_fraction":0,"disabled":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "agy")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' '"+report+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			p := New()
			p.lookPath = func(string) (string, error) { return bin, nil }
			p.now = func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
			if _, err := p.fetchCLIUsage(context.Background()); err == nil {
				t.Fatal("expected invalid report error")
			}
		})
	}
}

func TestCLIUsageCleansRedirectedDescendantsAfterSuccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process-state oracle")
	}
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	bin := filepath.Join(dir, "agy")
	report := `{"status":"SUCCESS","num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"command":{"name":"usage","data":{"groups":[{"name":"G","buckets":[{"id":"x","window":"weekly","remaining_fraction":0}]}]}}}`
	script := fmt.Sprintf("#!/bin/sh\nsleep 30 >/dev/null 2>&1 &\necho $! > %q\nprintf '%%s' '%s'\n", pidPath, report)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.lookPath = func(string) (string, error) { return bin, nil }
	if _, err := p.fetchCLIUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		// A process reaped during the read can return ESRCH instead of ENOENT.
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		end := strings.LastIndex(string(stat), ") ")
		if end >= 0 && strings.HasPrefix(string(stat)[end+2:], "Z ") {
			return
		}
	}
	process, _ := os.FindProcess(pid)
	if process != nil {
		_ = process.Kill()
	}
	t.Fatal("redirected CLI descendant survived successful report")
}

func TestCLIUsageBoundsAndRedactsFailures(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fake executable requires Unix")
	}
	for name, script := range map[string]string{
		"stderr":   "echo private-account-detail >&2; exit 4",
		"oversize": "head -c 1048577 /dev/zero",
	} {
		t.Run(name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "agy")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			p := New()
			p.lookPath = func(string) (string, error) { return bin, nil }
			_, err := p.fetchCLIUsage(context.Background())
			if err == nil || strings.Contains(err.Error(), "private-account-detail") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := New()
	p.lookPath = func(string) (string, error) { return "/bin/sh", nil }
	if _, err := p.fetchCLIUsage(ctx); err == nil {
		t.Fatal("ignored cancelled caller")
	}
}

func TestNativeCLIFailureDoesNotUseIncompleteRemoteSummary(t *testing.T) {
	p := New()
	p.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	p.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("fell back to incomplete remote summary")
		return nil, nil
	})
	if _, err := p.FetchUsage(context.Background()); err == nil {
		t.Fatal("missing CLI returned healthy usage")
	}
}
