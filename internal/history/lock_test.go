package history

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// The child reports the result of its first OS lock attempt, so a missing lock
// fails the oracle even when the scheduler happens to serialize both writers.
func TestHistoryProcess(t *testing.T) {
	mode := os.Getenv("CLAWMETER_HISTORY_PROCESS")
	if mode == "" {
		return
	}
	dir := os.Getenv("CLAWMETER_HISTORY_DIR")
	if mode == "hold" || mode == "torn-tail" || mode == "complete-tail" || mode == "flush-error" {
		lock, err := acquireLock(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if mode != "hold" {
			writeInterruptedTail(t, dir, mode)
		}
		fmt.Println("holding")
		if _, err := os.Stdin.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		return
	}
	firstAttempt := true
	tryLock = func(file *os.File) (bool, error) {
		locked, err := tryFileLock(file)
		if firstAttempt {
			if err != nil {
				fmt.Println("lock-error")
			} else if locked {
				fmt.Println("acquired")
			} else {
				fmt.Println("contended")
			}
			firstAttempt = false
		}
		return locked, err
	}
	if mode == "timeout" {
		lock, err := acquireLock(dir)
		if err == nil {
			lock.Close()
			t.Fatal("acquired a held lock")
		}
		if !strings.Contains(err.Error(), "timed out waiting for history lock") {
			t.Fatal(err)
		}
		fmt.Println("timeout")
		return
	}
	if mode == "read" {
		if _, err := Read(dir, time.Time{}, ""); err != nil {
			t.Fatal(err)
		}
		return
	}
	if limit := os.Getenv("CLAWMETER_HISTORY_CAP"); limit != "" {
		var err error
		maxHistoryBytes, err = strconv.ParseInt(limit, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	used := 20.0
	if value := os.Getenv("CLAWMETER_HISTORY_USED"); value != "" {
		var err error
		used, err = strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(used, at)}, at); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryOperationsWaitForHeldLock(t *testing.T) {
	for _, mode := range []string{"append", "read", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			lock, err := acquireLock(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if lock != nil {
					lock.Close()
				}
			})
			child := startHistoryProcess(t, dir, mode)
			child.expect(t, "contended")
			if got := countLines(t, dir); got != 0 {
				t.Fatalf("held-lock writer modified history: %d lines", got)
			}
			if mode == "timeout" {
				child.expect(t, "timeout")
			} else {
				if err := lock.Close(); err != nil {
					t.Fatal(err)
				}
				lock = nil
			}
			child.wait(t)
		})
	}
}

func TestSameKeyWritersDedupeAndRetainUnderContention(t *testing.T) {
	for _, retention := range []bool{false, true} {
		t.Run(fmt.Sprintf("retention=%t", retention), func(t *testing.T) {
			dir := t.TempDir()
			oldCap := maxHistoryBytes
			t.Cleanup(func() { maxHistoryBytes = oldCap })
			maxHistoryBytes = 400
			if retention {
				at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
				if err := Append(dir, map[string]*provider.UsageData{"work": dataAt(19, at)}, at); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := acquireLock(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if lock != nil {
					lock.Close()
				}
			})
			first := startHistoryProcess(t, dir, "append", "CLAWMETER_HISTORY_CAP=400")
			second := startHistoryProcess(t, dir, "append", "CLAWMETER_HISTORY_CAP=400")
			first.expect(t, "contended")
			second.expect(t, "contended")
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			lock = nil
			first.wait(t)
			second.wait(t)
			readings, err := Read(dir, time.Time{}, "")
			if err != nil || len(readings) != 1 || readings[0].UsedPercent != 20 {
				t.Fatalf("contending same-key writers: %#v, %v", readings, err)
			}
			assertHistorySizeAndLines(t, dir)
		})
	}
}

func TestHistoryLockReleasedAfterOwnerDeath(t *testing.T) {
	dir := t.TempDir()
	child := startHistoryProcess(t, dir, "hold")
	child.expect(t, "holding")
	file, err := os.OpenFile(filepath.Join(dir, ".usage-history.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if locked, err := tryFileLock(file); err != nil || locked {
		t.Fatalf("second process acquired held lock: %t, %v", locked, err)
	}
	if err := child.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.cmd.Wait(); err == nil {
		t.Fatal("killed owner exited successfully")
	}
	if locked, err := tryFileLock(file); err != nil || !locked {
		t.Fatalf("lock not released after owner death: %t, %v", locked, err)
	}
	if err := unlockFile(file); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

type historyProcess struct {
	cmd    *exec.Cmd
	stdout *bufio.Reader
	stdin  io.WriteCloser
	stderr bytes.Buffer
}

func startHistoryProcess(t *testing.T, dir, mode string, extraEnv ...string) *historyProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	child := &historyProcess{cmd: exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHistoryProcess$")}
	child.cmd.Env = append(os.Environ(), "CLAWMETER_HISTORY_PROCESS="+mode, "CLAWMETER_HISTORY_DIR="+dir)
	child.cmd.Env = append(child.cmd.Env, extraEnv...)
	child.cmd.Stderr = &child.stderr
	stdout, err := child.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.stdout = bufio.NewReader(stdout)
	child.stdin, err = child.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.cmd.ProcessState == nil {
			child.cmd.Process.Kill()
			child.cmd.Wait()
		}
		child.stdin.Close()
	})
	return child
}

func (child *historyProcess) expect(t *testing.T, want string) {
	t.Helper()
	line, err := child.stdout.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != want {
		t.Fatalf("child signal = %q, %v; want %q", line, err, want)
	}
}

func (child *historyProcess) wait(t *testing.T) {
	t.Helper()
	output, readErr := io.ReadAll(child.stdout)
	if err := child.cmd.Wait(); err != nil {
		t.Fatalf("history child: %v: %s%s", err, output, child.stderr.String())
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
}
