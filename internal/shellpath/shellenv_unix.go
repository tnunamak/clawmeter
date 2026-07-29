//go:build !windows

package shellpath

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const envMarker = "__CLAWMETER_ENV__"

func resolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	values, missing := inheritedEnv(request.EnvNames)
	if !request.AllowSessionEnvironmentFallback || len(missing) == 0 {
		return values
	}

	shell := authoritativeLoginShell()
	if shell == "" {
		return values
	}
	for name, value := range captureMissingEnvFromShell(shell, missing) {
		if value != "" {
			values[name] = value
		}
	}
	return values
}

func captureMissingEnvFromShell(shell string, names []string) map[string]string {
	probe, ok := probeForShell(shell)
	if !ok {
		return nil
	}

	args := []string{"-l", "-i", "-c", envProbeCommand(names, false)}
	if !terminalAvailable() && filepath.Base(shell) == "zsh" {
		args = []string{"-l", "-c", envProbeCommand(names, true)}
	}
	out, _ := runShellCommand(context.Background(), shell, args, envProbeTimeout(probe))
	return parseMarkedEnv(out, names)
}

func envProbeTimeout(probe shellProbe) time.Duration {
	if probe.recoveryTimeout > 0 && !terminalAvailable() {
		return probe.recoveryTimeout
	}
	return probe.interactiveTimeout
}

func envProbeCommand(names []string, sourceZshrc bool) string {
	var command strings.Builder
	if sourceZshrc {
		command.WriteString(`source "${ZDOTDIR:-$HOME}/.zshrc" >/dev/null 2>&1 || true; `)
	}
	for _, name := range names {
		command.WriteString("printf '")
		command.WriteString(envMarker)
		command.WriteString(name)
		command.WriteString("\\0%s\\0' '")
		command.WriteString(envMarker)
		command.WriteString(name)
		command.WriteString("' \"$")
		command.WriteString(name)
		command.WriteString("\"; ")
	}
	return strings.TrimSuffix(command.String(), "; ")
}

func parseMarkedEnv(out []byte, names []string) map[string]string {
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		requested[name] = true
	}
	values := make(map[string]string)
	parts := bytes.Split(out, []byte{0})
	for i := 0; i+1 < len(parts); i++ {
		if !bytes.HasPrefix(parts[i], []byte(envMarker)) {
			continue
		}
		name := string(parts[i][len(envMarker):])
		if requested[name] {
			values[name] = string(parts[i+1])
		}
	}
	return values
}
