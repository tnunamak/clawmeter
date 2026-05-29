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
	"io"
	"os"
	"os/exec"
	"os/user"
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
	for _, shell := range loginShellCandidates() {
		if path := capturePathFromShell(shell); len(path) > 0 {
			return path
		}
	}
	return nil
}

func capturePathFromShell(shell string) []string {
	// Use a marker to extract PATH cleanly from any shell noise.
	marker := "__CLAWMETER_PATH__"
	cmd := fmt.Sprintf(`printf '%s%%s%s' "$PATH"`, marker, marker)

	ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
	defer cancel()

	proc := exec.CommandContext(ctx, shell, "-l", "-i", "-c", cmd)
	proc.Stdin = nil
	// Suppress stderr (shell init may print warnings/motd)
	proc.Stderr = io.Discard

	// Some shell init files print the marker and then exit nonzero because
	// interactive-only commands failed. Keep stdout if it contains the PATH.
	out, _ := proc.Output()

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

func loginShellCandidates() []string {
	candidates := []string{
		os.Getenv("SHELL"),
		passwdLoginShell(),
		"/bin/zsh",
		"/usr/bin/zsh",
		"/opt/homebrew/bin/zsh",
		"/bin/bash",
		"/usr/bin/bash",
		"/bin/sh",
	}
	return existingUniqueShells(candidates)
}

func existingUniqueShells(candidates []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(candidates))
	for _, shell := range candidates {
		shell = strings.TrimSpace(shell)
		if shell == "" || seen[shell] {
			continue
		}
		if info, err := os.Stat(shell); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		seen[shell] = true
		out = append(out, shell)
	}
	return out
}

func passwdLoginShell() string {
	u, err := user.Current()
	if err != nil || u == nil {
		return ""
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	return loginShellFromPasswd(u.Uid, u.Username, string(data))
}

func loginShellFromPasswd(uid, username, passwd string) string {
	shortName := username
	if i := strings.LastIndexAny(shortName, `\/`); i >= 0 {
		shortName = shortName[i+1:]
	}
	for _, line := range strings.Split(passwd, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}
		if parts[2] == uid || parts[0] == username || parts[0] == shortName {
			return parts[6]
		}
	}
	return ""
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
