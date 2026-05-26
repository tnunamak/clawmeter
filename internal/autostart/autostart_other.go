//go:build !linux && !darwin && !windows

package autostart

import (
	"fmt"
	"runtime"
)

const supported = false

func install(string) error      { return fmt.Errorf("autostart not supported on %s", runtime.GOOS) }
func uninstall() error          { return fmt.Errorf("autostart not supported on %s", runtime.GOOS) }
func isInstalled() bool         { return false }
