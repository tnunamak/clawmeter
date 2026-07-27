//go:build darwin || linux

package shellpath

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processWaitDelay = 250 * time.Millisecond

type shellCommandResult struct {
	out []byte
	err error
}

// runShellCommand bounds capture at timeout plus processWaitDelay. Before
// Wait starts, group signaling is safe because the root PID cannot be reused;
// after stdout EOF, only the root is killed because no process retains stdout.
func runShellCommand(ctx context.Context, shell string, args []string, timeout time.Duration) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer readPipe.Close()

	proc := exec.Command(shell, args...)
	proc.Stdin = nil
	proc.Stdout = writePipe
	proc.Stderr = io.Discard
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := proc.Start(); err != nil {
		_ = writePipe.Close()
		return nil, err
	}
	_ = writePipe.Close()

	readDone := make(chan shellCommandResult, 1)
	go func() {
		var output bytes.Buffer
		_, readErr := io.Copy(&output, readPipe)
		readDone <- shellCommandResult{out: output.Bytes(), err: readErr}
	}()

	select {
	case result := <-readDone:
		// No process retains stdout. Waiting is now safe; a timeout can only
		// require killing this root process, never an entire process group.
		return waitAfterStdoutEOF(commandCtx, proc, readPipe, result)
	case <-commandCtx.Done():
		// Wait has not started. Kill the private group before reaping the root.
		if err := syscall.Kill(-proc.Process.Pid, syscall.SIGKILL); err != nil {
			_ = proc.Process.Kill()
		}
		_ = readPipe.Close()
		return reapAfterGroupKill(commandCtx.Err(), proc, readDone)
	}
}

func waitAfterStdoutEOF(ctx context.Context, proc *exec.Cmd, readPipe *os.File, result shellCommandResult) ([]byte, error) {
	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()
	select {
	case waitErr := <-waitDone:
		_ = readPipe.Close()
		return result.out, chooseCommandError(ctx, waitErr)
	case <-ctx.Done():
		_ = proc.Process.Kill()
		_ = readPipe.Close()
		select {
		case waitErr := <-waitDone:
			return result.out, chooseCommandError(ctx, waitErr)
		case <-time.After(processWaitDelay):
			return result.out, ctx.Err()
		}
	}
}

func reapAfterGroupKill(timeoutErr error, proc *exec.Cmd, readDone <-chan shellCommandResult) ([]byte, error) {
	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()
	var output []byte
	deadline := time.NewTimer(processWaitDelay)
	defer deadline.Stop()
	for waitDone != nil || readDone != nil {
		select {
		case <-waitDone:
			waitDone = nil
		case result := <-readDone:
			output = result.out
			readDone = nil
		case <-deadline.C:
			return output, timeoutErr
		}
	}
	return output, timeoutErr
}

func chooseCommandError(ctx context.Context, waitErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return waitErr
}
