//go:build !windows

package update

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func detachRestartCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func waitForRestartParent(pid int, timeout time.Duration) {
	if pid <= 0 {
		time.Sleep(restartDelay)
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
