//go:build !windows

package anthropic

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A wrapper shim (npm's claude.cmd on Windows, volta-style shims on Unix) runs
// the real CLI as a child. On timeout only the direct process is killed, so the
// child keeps running past the 30 s budget (and on Windows keeps the temp work
// dir busy, so RemoveAll fails and the dir leaks).
func TestDoctorTimeoutKillsShimChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	shim := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nsleep 30 &\necho $! > \"$PIDFILE\"\nwait\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = doctorCommand(ctx, shim, dir, append(os.Environ(), "PIDFILE="+pidFile)).Run()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	time.Sleep(100 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("doctor child pid %d still running after timeout", pid)
	}
}
