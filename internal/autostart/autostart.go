// Package autostart manages launch-at-login for the clawmeter tray on Linux
// (XDG .desktop), macOS (LaunchAgent), and Windows (registry Run key).
//
// Each platform-specific file (autostart_linux.go, autostart_darwin.go,
// autostart_windows.go) implements the same three primitives:
// install(bin string), uninstall(), isInstalled() bool. This file holds the
// public API and the bin-path resolution that's the same everywhere.
package autostart

import (
	"os"
	"path/filepath"
)

// IsSupported reports whether autostart is implemented for the current OS.
// Tray UI uses this to decide whether to expose the toggle at all.
func IsSupported() bool {
	return supported
}

// Install enables launch-at-login for the current executable.
func Install() error {
	bin, err := execPath()
	if err != nil {
		return err
	}
	return install(bin)
}

// IsInstalled reports whether the autostart entry currently exists.
func IsInstalled() bool {
	return isInstalled()
}

// Uninstall removes the autostart entry.
func Uninstall() error {
	return uninstall()
}

func execPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// On Windows, EvalSymlinks can fail for paths that contain no
		// symlinks at all (older bug in stdlib); fall back to the raw
		// exe path, which is already an absolute path on every OS that
		// os.Executable() supports.
		return exe, nil
	}
	return resolved, nil
}
