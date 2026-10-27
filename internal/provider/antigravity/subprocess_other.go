//go:build !windows

package antigravity

import (
	"os/exec"
	"syscall"
)

func hideSubprocessWindow(cmd *exec.Cmd) {}

func configureQuotaCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

func startQuotaCommand(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// A successful parent can leave children with redirected standard streams.
	// Cancel alone would stop watching once Wait returned.
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }, nil
}
