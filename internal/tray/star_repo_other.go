//go:build !windows

package tray

import "os/exec"

func hideGHWindow(*exec.Cmd) {}
