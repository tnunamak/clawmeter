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
	"path/filepath"
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
	shell := authoritativeLoginShell()
	if shell == "" {
		return nil
	}
	if path := capturePathFromShell(shell); len(path) > 0 {
		return path
	}
	return nil
}

func capturePathFromShell(shell string) []string {
	probe, ok := probeForShell(shell)
	if !ok {
		return nil
	}

	run := func(args ...string) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
		defer cancel()

		proc := exec.CommandContext(ctx, shell, args...)
		proc.Stdin = nil
		// Suppress stderr (shell init may print warnings/motd)
		proc.Stderr = io.Discard
		out, _ := proc.Output()
		return out
	}

	// Some shell init files print the marker and then exit nonzero because
	// interactive-only commands failed. Keep stdout if it contains the PATH.
	out := run(probe.loginInteractiveArgs...)
	if path := parseMarkedPath(out, marker); len(path) > 0 {
		return path
	}

	if filepath.Base(shell) != "zsh" {
		return nil
	}

	// A GUI/non-TTY launch can make zsh startup fail before it reaches the
	// marker. Source zshrc explicitly in a bounded non-interactive shell so
	// PATH changes are still captured.
	out = run(probe.zshRecoveryArgs...)
	return parseMarkedPath(out, marker)
}

const marker = "__CLAWMETER_PATH__"

type shellProbe struct {
	loginInteractiveArgs []string
	zshRecoveryArgs      []string
}

func probeForShell(shell string) (shellProbe, bool) {
	base := filepath.Base(shell)
	posix := fmt.Sprintf(`printf '%s%%s%s' "$PATH"`, marker, marker)
	probes := map[string]shellProbe{
		"zsh":  {loginInteractiveArgs: []string{"-l", "-i", "-c", posix}},
		"bash": {loginInteractiveArgs: []string{"-l", "-i", "-c", posix}},
		"sh":   {loginInteractiveArgs: []string{"-l", "-i", "-c", posix}},
		"dash": {loginInteractiveArgs: []string{"-l", "-i", "-c", posix}},
		"ksh":  {loginInteractiveArgs: []string{"-l", "-i", "-c", posix}},
		// fish PATH is a list; string join turns it into the platform PATH
		// representation before printf emits the marker-delimited value.
		"fish": {loginInteractiveArgs: []string{"-l", "-i", "-c", fmt.Sprintf(`printf '%s%%s%s' (string join : -- $PATH)`, marker, marker)}},
	}
	probe, ok := probes[base]
	if !ok {
		return shellProbe{}, false
	}
	if base == "zsh" {
		fallback := fmt.Sprintf(`source "${ZDOTDIR:-$HOME}/.zshrc" >/dev/null 2>&1 || true; printf '%s%%s%s' "$PATH"`, marker, marker)
		probe.zshRecoveryArgs = []string{"-l", "-c", fallback}
	}
	return probe, true
}

func parseMarkedPath(out []byte, marker string) []string {
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
	return loginShellCandidatesFrom(os.Getenv("SHELL"), passwdLoginShell())
}

func authoritativeLoginShell() string {
	return authoritativeLoginShellFrom(loginShellCandidates())
}

func authoritativeLoginShellFrom(candidates []string) string {
	for _, shell := range candidates {
		if resolved, err := exec.LookPath(shell); err == nil {
			if _, supported := probeForShell(resolved); supported {
				return resolved
			}
		}
	}
	return ""
}

func loginShellCandidatesFrom(shell, passwdShell string) []string {
	candidates := []string{shell, passwdShell, "/bin/sh"}
	seen := make(map[string]bool, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
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
		key := pathEntryKey(p)
		if key != "" && !seen[key] {
			seen[key] = true
			parts = append(parts, p)
		}
	}

	for _, p := range extra {
		key := pathEntryKey(p)
		if key != "" && !seen[key] {
			seen[key] = true
			parts = append(parts, p)
		}
	}

	os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}
