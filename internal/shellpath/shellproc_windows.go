//go:build windows

package shellpath

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

func runShellCommand(ctx context.Context, shell string, args []string, timeout time.Duration) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	proc := exec.CommandContext(commandCtx, shell, args...)
	var stdout bytes.Buffer
	proc.Stdin = nil
	proc.Stdout = &stdout
	proc.Stderr = io.Discard
	err := proc.Run()
	if commandCtx.Err() != nil {
		return stdout.Bytes(), commandCtx.Err()
	}
	return stdout.Bytes(), err
}
