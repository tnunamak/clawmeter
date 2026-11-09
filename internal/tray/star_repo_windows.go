//go:build windows

package tray

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// hideGHWindow keeps gh from opening a console window from the tray app.
func hideGHWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
