//go:build darwin || linux

package shellpath

import (
	"bytes"
	"context"
	"errors"
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

// runShellCommand bounds capture at timeout plus processWaitDelay. The shell
// owns a private process group so every same-group descendant is terminated
// before the root is reaped. Escaped descendants cannot hold this function
// open after stdout EOF, but are outside the cleanup guarantee.
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
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = readPipe.Close()
		_ = writePipe.Close()
		return nil, err
	}
	proc.Stderr = stderr
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := proc.Start(); err != nil {
		_ = stderr.Close()
		_ = writePipe.Close()
		return nil, err
	}
	_ = stderr.Close()
	_ = writePipe.Close()

	readDone := make(chan shellCommandResult, 1)
	go func() {
		var output bytes.Buffer
		_, readErr := io.Copy(&output, readPipe)
		readDone <- shellCommandResult{out: output.Bytes(), err: readErr}
	}()

	select {
	case result := <-readDone:
		return waitAfterStdoutEOF(commandCtx, proc, readPipe, result)
	case <-commandCtx.Done():
		// Wait has not started. Kill the private group before reaping the root.
		killPrivateProcessGroup(proc)
		_ = readPipe.Close()
		return reapAfterGroupKill(commandCtx.Err(), proc, readDone)
	}
}

func waitAfterStdoutEOF(ctx context.Context, proc *exec.Cmd, readPipe *os.File, result shellCommandResult) ([]byte, error) {
	// EOF only proves that no process currently holds the capture pipe. The
	// root may still be alive, so observe its exit without reaping it first.
	// This keeps the group signal before Wait in both the normal and canceled
	// paths, while preserving the root's real exit status on normal completion.
	// A failed liveness observation returns immediately, so cleanup remains
	// bounded and Wait reports the root's actual result.
	waitForRootExitOrContext(ctx, proc.Process.Pid)
	killPrivateProcessGroup(proc)

	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()
	reapTimer := time.NewTimer(processWaitDelay)
	defer reapTimer.Stop()
	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-reapTimer.C:
		_ = readPipe.Close()
		return result.out, chooseCommandError(ctx, exec.ErrWaitDelay)
	}
	_ = readPipe.Close()
	return result.out, chooseCommandError(ctx, waitErr)
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

func killPrivateProcessGroup(proc *exec.Cmd) {
	if err := syscall.Kill(-proc.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		// If group signaling fails for a live root, retain the root-only fallback.
		// The root remains unreaped here, so this cannot target a reused PID.
		_ = proc.Process.Kill()
	}
}
