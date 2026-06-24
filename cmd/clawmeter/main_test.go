package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles clawmeter into a temp dir and returns the path.
// The binary is reused across subtests via t.Cleanup.
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "clawmeter")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\n%s", err, stderr.String())
	}
	return bin
}

// runWithHome invokes the binary with HOME pointed at an isolated dir so the
// developer's real ~/.config/clawmeter is untouched.
func runWithHome(t *testing.T, bin, home string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

func TestConfigEnable_RejectsUnknownProvider(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	_, stderr, code := runWithHome(t, bin, home, "config", "enable", "opneai")
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0 (stderr: %s)", stderr)
	}
	if !strings.Contains(stderr, "unknown provider") {
		t.Errorf("stderr missing 'unknown provider': %s", stderr)
	}
	if !strings.Contains(stderr, "openai") {
		t.Errorf("expected suggestion for 'openai', stderr: %s", stderr)
	}

	// Config file must not have been created with the bogus name.
	cfgPath := filepath.Join(home, ".config", "clawmeter", "config.yaml")
	if data, err := os.ReadFile(cfgPath); err == nil {
		if strings.Contains(string(data), "opneai") {
			t.Errorf("config.yaml contains the typo'd name: %s", data)
		}
	}
}

func TestConfigDisable_RejectsUnknownProvider(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	_, stderr, code := runWithHome(t, bin, home, "config", "disable", "claudee")
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0 (stderr: %s)", stderr)
	}
	if !strings.Contains(stderr, "unknown provider") {
		t.Errorf("stderr missing 'unknown provider': %s", stderr)
	}
}

func TestConfigEnable_AcceptsKnownProvider(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	stdout, stderr, code := runWithHome(t, bin, home, "config", "enable", "openai")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Enabled provider: openai") {
		t.Errorf("expected confirmation, got: %s", stdout)
	}
}

func TestConfigDisable_PersistsAndSurfacesInSingleProvider(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	// Disable openai.
	_, stderr, code := runWithHome(t, bin, home, "config", "disable", "openai")
	if code != 0 {
		t.Fatalf("disable: exit %d (%s)", code, stderr)
	}

	// Asking for the single-provider status should refuse with a clear message.
	_, stderr, code = runWithHome(t, bin, home, "openai", "--plain")
	if code == 0 {
		t.Fatalf("expected non-zero exit when querying disabled provider")
	}
	if !strings.Contains(stderr, "disabled") {
		t.Errorf("expected 'disabled' in stderr, got: %s", stderr)
	}
}

func TestProvidersList_DistinguishesDisabledFromDetected(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	// Disable a provider.
	if _, stderr, code := runWithHome(t, bin, home, "config", "disable", "openai"); code != 0 {
		t.Fatalf("disable: exit %d (%s)", code, stderr)
	}

	stdout, _, code := runWithHome(t, bin, home, "providers")
	if code != 0 {
		t.Fatalf("providers: exit %d", code)
	}
	if !strings.Contains(stdout, "disabled") {
		t.Errorf("expected 'disabled' in output: %s", stdout)
	}
}

func TestSetupAllDoesNotInstallTmuxByDefault(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	stdout, stderr, code := runWithHome(t, bin, home, "setup", "--dry-run", "--all")
	if code != 0 {
		t.Fatalf("setup --all: exit %d (%s)", code, stderr)
	}
	if strings.Contains(stdout, "tmux:") {
		t.Fatalf("setup --all should not include tmux by default: %s", stdout)
	}
	if !strings.Contains(stdout, "Claude Code statusline") {
		t.Fatalf("setup --all should include Claude Code statusline: %s", stdout)
	}
}

func TestTopLevelAllIsStatusShortcut(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	stdout, stderr, code := runWithHome(t, bin, home, "--all", "--plain")
	if code != 0 {
		t.Fatalf("clawmeter --all --plain: exit %d (%s)", code, stderr)
	}
	if strings.Contains(stderr, "unknown command") {
		t.Fatalf("--all should be handled as a status flag, stderr: %s", stderr)
	}
	if !strings.Contains(stdout, "Claude") {
		t.Fatalf("--all should include unavailable providers in status output: %s", stdout)
	}
}
