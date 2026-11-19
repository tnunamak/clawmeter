//go:build darwin || linux

package shellpath

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixtureDeadline is the command deadline for tests whose fixture must start a
// shell, fork a descendant, and publish its pid file before that deadline fires.
// A tight deadline races that setup: when the machine is busy, starting the
// shell and forking takes hundreds of milliseconds (measured p99 ~250 ms, max
// ~300 ms with every CPU busy), and a deadline that lands first kills the group
// before the fixture writes anything. The test then fails on the missing pid
// file with no product bug behind it.
const fixtureDeadline = 2 * time.Second

// boundedReturn is the wall-clock bound for a command that has hit its deadline
// or had its context canceled, and the poll budget for the fixtures below. It is
// far below the 30 s descendants these tests wait on, so a command that blocks on
// one still fails, and far above the millisecond-scale reaping delay, because a
// loaded machine can starve the reaping goroutine for seconds.
const boundedReturn = 10 * time.Second

// assertReturnedBoundedly fails when a command did not return within
// boundedReturn. A command that waits on a long-lived descendant trips this: it
// returns only once that descendant exits.
func assertReturnedBoundedly(t *testing.T, started time.Time, subject string) {
	t.Helper()
	if elapsed := time.Since(started); elapsed > boundedReturn {
		t.Fatalf("%s returned after %s, want within %s", subject, elapsed, boundedReturn)
	}
}

func TestRunShellCommandTimeoutKillsSameGroupDescendant(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	registerPIDFileCleanup(t, pidFile)
	// The root remains alive, so cancellation can safely kill its private group.
	script := "sleep 30 & child=$!; echo $child > " + shellQuote(pidFile) + "; wait"

	started := time.Now()
	_, err = runShellCommand(context.Background(), shell, []string{"-c", script}, fixtureDeadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runShellCommand error = %v, want deadline exceeded", err)
	}
	assertReturnedBoundedly(t, started, "timed-out command")

	data := waitForPIDFile(t, pidFile)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	waitForProcessGone(t, childPID)
}

func TestRunShellCommandRootExitWithPipeHoldingDescendantReturnsBoundedly(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	registerPIDFileCleanup(t, pidFile)
	script := "sleep 30 & child=$!; echo $child > " + shellQuote(pidFile)

	started := time.Now()
	_, _ = runShellCommand(context.Background(), shell, []string{"-c", script}, fixtureDeadline)
	assertReturnedBoundedly(t, started, "pipe-holding descendant")
	data := waitForPIDFile(t, pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	waitForProcessGone(t, pid)
}

func TestRunShellCommandSuccessKillsSameGroupClosedStdoutDescendant(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	registerPIDFileCleanup(t, pidFile)
	script := "sleep 30 >/dev/null 2>&1 & child=$!; echo $child > " + shellQuote(pidFile) + "; printf marker"

	out, err := runShellCommand(context.Background(), shell, []string{"-c", script}, boundedReturn)
	if err != nil {
		t.Fatalf("runShellCommand error = %v, want success", err)
	}
	if string(out) != "marker" {
		t.Fatalf("output = %q, want marker", out)
	}
	data := waitForPIDFile(t, pidFile)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	waitForProcessGone(t, childPID)
}

func TestRunShellCommandTimeoutAfterStdoutEOFKillsSameGroupDescendant(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	registerPIDFileCleanup(t, pidFile)
	// The root closes stdout, then remains alive waiting for a same-group child.
	script := "exec 1>&-; sleep 30 & child=$!; echo $child > " + shellQuote(pidFile) + "; wait"

	started := time.Now()
	_, err = runShellCommand(context.Background(), shell, []string{"-c", script}, fixtureDeadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runShellCommand error = %v, want deadline exceeded", err)
	}
	assertReturnedBoundedly(t, started, "timed-out command after stdout EOF")
	data := waitForPIDFile(t, pidFile)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	waitForProcessGone(t, childPID)
}

func TestRunShellCommandDirectlyKillsRootAfterStdoutEOF(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "root.pid")
	registerPIDFileCleanup(t, pidFile)

	started := time.Now()
	_, err = runShellCommand(context.Background(), shell, []string{"-c", "echo $$ > " + shellQuote(pidFile) + "; exec 1>&-; sleep 30"}, fixtureDeadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runShellCommand error = %v, want deadline exceeded", err)
	}
	assertReturnedBoundedly(t, started, "root without stdout")
}

func TestRunShellCommandCallerCancellationAfterStdoutEOF(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "root.pid")
	registerPIDFileCleanup(t, pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runShellCommand(ctx, shell, []string{"-c", "echo $$ > " + shellQuote(pidFile) + "; exec 1>&-; sleep 30"}, boundedReturn)
		done <- err
	}()
	pidData := waitForPIDFile(t, pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatalf("parse root pid %q: %v", pidData, err)
	}
	// The shell closes stdout before sleeping; allow that EOF to be observed,
	// then cancellation must still terminate the root promptly.
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runShellCommand error = %v, want context canceled", err)
		}
		if elapsed := time.Since(started); elapsed > boundedReturn {
			t.Fatalf("EOF-then-canceled command returned after %s", elapsed)
		}
		waitForProcessGone(t, pid)
	case <-time.After(boundedReturn):
		t.Fatal("EOF-then-canceled command did not return")
	}
}

func TestRunShellCommandEscapedDescendantStillReturnsBoundedly(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid not installed")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "escaped.pid")
	registerPIDFileCleanup(t, pidFile)
	script := "" + setsid + " sleep 30 & echo $! > " + shellQuote(pidFile)

	started := time.Now()
	_, err = runShellCommand(context.Background(), shell, []string{"-c", script}, fixtureDeadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runShellCommand error = %v, want deadline exceeded", err)
	}
	assertReturnedBoundedly(t, started, "escaped descendant")
	data := waitForPIDFile(t, pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse escaped pid %q: %v", data, err)
	}
	// Escaped descendants are not expected to be killed by production cleanup.
	_ = pid
}

func TestRunShellCommandEscapedDescendantUsesDirectStderrFile(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid not installed")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	tempDir := t.TempDir()
	pidFile := filepath.Join(tempDir, "escaped.pid")
	stderrFile := filepath.Join(tempDir, "stderr-target")
	registerPIDFileCleanup(t, pidFile)
	childScript := "test /dev/fd/2 -ef /dev/null && echo direct > " + shellQuote(stderrFile) + " || echo pipe > " + shellQuote(stderrFile) + "; sleep 30"
	script := shellQuote(setsid) + " " + shellQuote(shell) + " -c " + shellQuote(childScript) + " & child=$!; echo $child > " + shellQuote(pidFile) + "; wait"

	started := time.Now()
	_, err = runShellCommand(context.Background(), shell, []string{"-c", script}, fixtureDeadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runShellCommand error = %v, want deadline exceeded", err)
	}
	assertReturnedBoundedly(t, started, "escaped stderr holder")
	if got := strings.TrimSpace(string(waitForPIDFile(t, stderrFile))); got != "direct" {
		t.Fatalf("escaped child stderr target = %q, want direct /dev/null", got)
	}
}

func TestRunShellCommandSuccess(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	out, err := runShellCommand(context.Background(), shell, []string{"-c", "printf success"}, time.Second)
	if err != nil {
		t.Fatalf("runShellCommand error = %v", err)
	}
	if string(out) != "success" {
		t.Fatalf("output = %q, want success", out)
	}
}

func TestRunShellCommandPreservesStdoutAndExitErrorWhileSuppressingStderr(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	out, err := runShellCommand(context.Background(), shell, []string{"-c", "printf stdout; printf stderr >&2; exit 7"}, time.Second)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("runShellCommand error = %v, want *exec.ExitError", err)
	}
	if string(out) != "stdout" {
		t.Fatalf("stdout = %q, want stdout", out)
	}
}

func TestRunShellCommandAlreadyCanceledContext(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err = runShellCommand(ctx, shell, []string{"-c", "sleep 30"}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runShellCommand error = %v, want context canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("already-canceled command returned after %s", elapsed)
	}
}

func TestRunShellCommandCallerCancellationWhileRunning(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	registerPIDFileCleanup(t, ready)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runShellCommand(ctx, shell, []string{"-c", "echo $$ > " + shellQuote(ready) + "; sleep 30 & wait"}, boundedReturn)
		done <- err
	}()
	waitForPIDFile(t, ready)
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runShellCommand error = %v, want context canceled", err)
		}
		if elapsed := time.Since(started); elapsed > boundedReturn {
			t.Fatalf("caller-canceled command returned after %s", elapsed)
		}
	case <-time.After(boundedReturn):
		t.Fatal("caller-canceled command did not return")
	}
}

func waitForPIDFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(boundedReturn)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return data
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("PID file %q was not ready", path)
	return nil
}

func registerPIDFileCleanup(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(boundedReturn)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) || processIsZombie(pid) {
			return
		}
		if err != nil {
			t.Fatalf("check child pid: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant pid %d is still running", pid)
}

func processIsZombie(pid int) bool {
	if runtime.GOOS == "darwin" {
		state, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
		return err == nil && strings.HasPrefix(strings.TrimSpace(string(state)), "Z")
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), ") Z ")
}

func shellQuote(path string) string {
	return "'" + filepath.ToSlash(path) + "'"
}
