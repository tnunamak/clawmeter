//go:build !windows

package openai

import "os/exec"

func hideSubprocessWindow(cmd *exec.Cmd) {}
