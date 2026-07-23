//go:build !windows

package antigravity

import "os/exec"

func hideSubprocessWindow(cmd *exec.Cmd) {}
