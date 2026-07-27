//go:build !windows && !darwin && !linux

package shellpath

import (
	"context"
	"io"
	"os/exec"
	"time"
)

// Unsupported Unix-like targets retain the old fail-soft command behavior;
// the process-group implementation is enabled only where Setpgid and
// negative-PID signaling are available.
func runShellCommand(ctx context.Context, shell string, args []string, timeout time.Duration) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	proc := exec.CommandContext(commandCtx, shell, args...)
	proc.Stdin = nil
	proc.Stderr = io.Discard
	return proc.Output()
}
