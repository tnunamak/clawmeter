//go:build windows

package update

import (
	"os/exec"
	"syscall"
	"time"
)

const (
	windowsDetachedProcess       = 0x00000008
	windowsCreateNewProcessGroup = 0x00000200
	windowsSynchronize           = 0x00100000
	windowsInfinite              = 0xffffffff
)

func detachRestartCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsDetachedProcess | windowsCreateNewProcessGroup,
	}
}

func waitForRestartParent(pid int, timeout time.Duration) {
	if pid <= 0 {
		time.Sleep(restartDelay)
		return
	}
	handle, err := syscall.OpenProcess(windowsSynchronize, false, uint32(pid))
	if err != nil {
		time.Sleep(restartDelay)
		return
	}
	defer syscall.CloseHandle(handle)

	millis := uint32(timeout / time.Millisecond)
	if timeout <= 0 {
		millis = windowsInfinite
	}
	syscall.WaitForSingleObject(handle, millis)
}
