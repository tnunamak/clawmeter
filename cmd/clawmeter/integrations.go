package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const clawmeterStatuslineCommand = "clawmeter statusline"

type integrationResult struct {
	Name    string
	Status  string
	Detail  string
	Changed bool
}

func setupTmuxIntegration(dryRun bool) integrationResult {
	if runtime.GOOS == "windows" {
		return integrationResult{Name: "tmux", Status: "skipped", Detail: "tmux integration is not supported on Windows"}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return integrationResult{Name: "tmux", Status: "skipped", Detail: "tmux not found on PATH"}
	}
	if os.Getenv("TMUX") == "" {
		return integrationResult{Name: "tmux", Status: "skipped", Detail: "not running inside tmux"}
	}

	current, err := tmuxStatusRight()
	if err != nil {
		return integrationResult{Name: "tmux", Status: "error", Detail: err.Error()}
	}
	next, changed := tmuxStatusRightWithClawmeter(current)
	if !changed {
		return integrationResult{Name: "tmux", Status: "ok", Detail: "status-right already includes clawmeter"}
	}
	if dryRun {
		return integrationResult{Name: "tmux", Status: "would change", Detail: next, Changed: true}
	}

	if backup, err := backupTmuxStatusRight(current); err == nil && backup != "" {
		fmt.Printf("tmux backup: %s\n", backup)
	}
	if err := exec.Command("tmux", "set", "-g", "status-right", next).Run(); err != nil {
		return integrationResult{Name: "tmux", Status: "error", Detail: fmt.Sprintf("set status-right: %v", err)}
	}
	_ = exec.Command("tmux", "refresh-client", "-S").Run()
	return integrationResult{Name: "tmux", Status: "installed", Detail: "prepended clawmeter statusline", Changed: true}
}

func setupClaudeStatuslineIntegration(dryRun bool) integrationResult {
	path, err := claudeSettingsPath()
	if err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}

	var data []byte
	if existing, err := os.ReadFile(path); err == nil {
		data = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}

	next, changed, err := mergeClaudeStatusLine(data)
	if err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}
	if !changed {
		return integrationResult{Name: "Claude Code statusline", Status: "ok", Detail: path}
	}
	if dryRun {
		return integrationResult{Name: "Claude Code statusline", Status: "would change", Detail: path, Changed: true}
	}

	if len(data) > 0 {
		if backup, err := backupFile(path, "before-clawmeter-statusline"); err == nil {
			fmt.Printf("Claude backup: %s\n", backup)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}
	if err := os.WriteFile(path, next, 0o644); err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}
	return integrationResult{Name: "Claude Code statusline", Status: "installed", Detail: path, Changed: true}
}

func tmuxStatusRightWithClawmeter(existing string) (string, bool) {
	if strings.Contains(existing, clawmeterStatuslineCommand) {
		return existing, false
	}
	prefix := "#[fg=#9ece6a]#(" + clawmeterStatuslineCommand + ") #[fg=#565f89]| "
	return prefix + existing, true
}

func tmuxStatusRight() (string, error) {
	out, err := exec.Command("tmux", "show", "-gqv", "status-right").Output()
	if err != nil {
		return "", fmt.Errorf("read tmux status-right: %w", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func backupTmuxStatusRight(current string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "clawmeter", "tmux-status-right.before-clawmeter")
	if _, err := os.Stat(path); err == nil {
		return "", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(current), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.local.json"), nil
}

func mergeClaudeStatusLine(data []byte) ([]byte, bool, error) {
	settings := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, false, fmt.Errorf("parse Claude settings: %w", err)
		}
	}

	statusLine := map[string]any{
		"type":    "command",
		"command": clawmeterStatuslineCommand,
	}
	if existing, ok := settings["statusLine"].(map[string]any); ok {
		if existing["type"] == statusLine["type"] && existing["command"] == statusLine["command"] {
			return appendTrailingNewline(data), false, nil
		}
	}

	settings["statusLine"] = statusLine
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return appendTrailingNewline(out), true, nil
}

func appendTrailingNewline(data []byte) []byte {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data
	}
	out := make([]byte, 0, len(data)+1)
	out = append(out, data...)
	out = append(out, '\n')
	return out
}

func backupFile(path, suffix string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	backup := fmt.Sprintf("%s.%s.%s", path, suffix, time.Now().Format("20060102150405"))
	if err := os.WriteFile(backup, data, 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

func tmuxIntegrationStatus() integrationResult {
	if runtime.GOOS == "windows" {
		return integrationResult{Name: "tmux", Status: "skipped", Detail: "not supported on Windows"}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return integrationResult{Name: "tmux", Status: "not found", Detail: "tmux not found on PATH"}
	}
	if os.Getenv("TMUX") == "" {
		return integrationResult{Name: "tmux", Status: "available", Detail: "run setup inside tmux to install status-right"}
	}
	current, err := tmuxStatusRight()
	if err != nil {
		return integrationResult{Name: "tmux", Status: "error", Detail: err.Error()}
	}
	if strings.Contains(current, clawmeterStatuslineCommand) {
		return integrationResult{Name: "tmux", Status: "installed", Detail: "status-right includes clawmeter"}
	}
	return integrationResult{Name: "tmux", Status: "available", Detail: "run clawmeter setup --tmux"}
}

func claudeStatuslineStatus() integrationResult {
	path, err := claudeSettingsPath()
	if err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return integrationResult{Name: "Claude Code statusline", Status: "available", Detail: "run clawmeter setup --claude-statusline"}
	}
	if err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}
	next, changed, err := mergeClaudeStatusLine(data)
	if err != nil {
		return integrationResult{Name: "Claude Code statusline", Status: "error", Detail: err.Error()}
	}
	if !changed || bytes.Equal(next, appendTrailingNewline(data)) {
		return integrationResult{Name: "Claude Code statusline", Status: "installed", Detail: path}
	}
	return integrationResult{Name: "Claude Code statusline", Status: "available", Detail: "run clawmeter setup --claude-statusline"}
}

func printIntegrationResult(result integrationResult) {
	line := fmt.Sprintf("  %-24s %s", result.Name+":", result.Status)
	if result.Detail != "" {
		line += " - " + result.Detail
	}
	fmt.Println(line)
}
