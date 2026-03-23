// Package shellpath captures the user's login shell PATH so that tray/GUI
// processes can find binaries installed via nvm, fnm, mise, homebrew, etc.
//
// On Linux/macOS the desktop-entry or LaunchAgent that starts the tray does
// not source .zshrc/.bashrc, so tools like codex and gemini (installed via
// npm) are invisible. We fix this by running `$SHELL -l -i -c 'echo $PATH'`
// once and merging the result into the process environment.
//
// On Windows PATH is set via the registry and inherited by all processes,
// so this is a no-op.
package shellpath

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const captureTimeout = 3 * time.Second

var (
	once     sync.Once
	captured []string
)

// Init captures the login shell PATH and merges it into os.Environ().
// Safe to call multiple times — only the first call does work.
// No-op on Windows.
func Init() {
	if runtime.GOOS == "windows" {
		return
	}
	once.Do(func() {
		captured = capture()
		if len(captured) > 0 {
			merge(captured)
		}
	})
}

// capture runs the user's login shell to get PATH.
func capture() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	// Use a marker to extract PATH cleanly from any shell noise.
	marker := "__CLAWMETER_PATH__"
	cmd := fmt.Sprintf(`printf '%s%%s%s' "$PATH"`, marker, marker)

	ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
	defer cancel()

	proc := exec.CommandContext(ctx, shell, "-l", "-i", "-c", cmd)
	proc.Stdin = nil
	// Suppress stderr (shell init may print warnings/motd)
	proc.Stderr = nil

	out, err := proc.Output()
	if err != nil {
		return nil
	}

	// Extract the PATH between markers.
	markerBytes := []byte(marker)
	start := bytes.Index(out, markerBytes)
	if start < 0 {
		return nil
	}
	rest := out[start+len(markerBytes):]
	end := bytes.Index(rest, markerBytes)
	if end < 0 {
		return nil
	}

	pathStr := string(rest[:end])
	if pathStr == "" {
		return nil
	}

	return strings.Split(pathStr, ":")
}

// merge adds login shell PATH entries to the current process PATH,
// deduplicating and preserving order (existing entries first).
func merge(loginPaths []string) {
	existing := os.Getenv("PATH")
	seen := make(map[string]bool)
	var parts []string

	// Keep existing entries first.
	for _, p := range strings.Split(existing, ":") {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}

	// Append new entries from the login shell.
	for _, p := range loginPaths {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}

	os.Setenv("PATH", strings.Join(parts, ":"))
}
