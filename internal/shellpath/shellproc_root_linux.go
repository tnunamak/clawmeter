//go:build linux

package shellpath

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func rootProcessExited(pid int) (bool, error) {
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, pid, &info, syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT, nil); err != nil {
		return false, err
	}
	return info.Signo != 0, nil
}
