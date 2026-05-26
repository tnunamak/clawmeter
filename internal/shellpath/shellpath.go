// Package shellpath ensures the tray process sees the user's full PATH,
// not the stale snapshot it inherits from its launcher.
//
// On Linux/macOS the .desktop / LaunchAgent that starts the tray does not
// source .zshrc/.bashrc, so tools installed via nvm, fnm, mise, homebrew,
// or npm-global (codex, gemini) are invisible. We fix this by running the
// login shell once and merging the result into the process environment.
//
// On Windows, Explorer (which spawns the tray when the user double-clicks
// the Start Menu shortcut or via the HKCU\...\Run autostart key) caches
// its environment at login. Apps installed *after* login — winget, scoop,
// or our own installer's PATH edit — don't take effect for tray processes
// until the user logs out and back in. We fix this by reading the user
// PATH directly from the registry and merging it into the process env.
package shellpath

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const captureTimeout = 3 * time.Second

var (
	once     sync.Once
	captured []string
)

// Init captures the authoritative PATH (login-shell on Unix, registry on
// Windows) and merges it into os.Environ(). Safe to call multiple times —
// only the first call does work.
func Init() {
	once.Do(func() {
		captured = capture()
		if len(captured) > 0 {
			merge(captured)
		}
	})
}

// captureLoginShell runs the user's login shell to get PATH.
func captureLoginShell() []string {
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

	return strings.Split(pathStr, string(os.PathListSeparator))
}

// merge adds captured PATH entries to the current process PATH,
// deduplicating and preserving order (existing entries first).
func merge(extra []string) {
	existing := os.Getenv("PATH")
	seen := make(map[string]bool)
	var parts []string

	for _, p := range strings.Split(existing, string(os.PathListSeparator)) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}

	for _, p := range extra {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}

	os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}
