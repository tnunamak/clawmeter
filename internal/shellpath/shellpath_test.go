package shellpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMergeDeduplicates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not applicable on Windows")
	}

	orig := os.Getenv("PATH")
	defer os.Setenv("PATH", orig)

	os.Setenv("PATH", "/usr/bin:/usr/local/bin")
	merge([]string{"/usr/bin", "/home/user/.nvm/bin", "/usr/local/bin", "/opt/new"})

	got := os.Getenv("PATH")
	parts := strings.Split(got, ":")

	// Original entries should come first, in order
	if parts[0] != "/usr/bin" || parts[1] != "/usr/local/bin" {
		t.Errorf("original entries reordered: %v", parts)
	}

	// New entries appended
	if !strings.Contains(got, "/home/user/.nvm/bin") {
		t.Error("missing /home/user/.nvm/bin")
	}
	if !strings.Contains(got, "/opt/new") {
		t.Error("missing /opt/new")
	}

	// No duplicates
	seen := make(map[string]int)
	for _, p := range parts {
		seen[p]++
		if seen[p] > 1 {
			t.Errorf("duplicate entry: %s", p)
		}
	}
}

func TestInitNoopOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only applicable on Windows")
	}
	// Should not panic
	Init()
}

func TestCapturePathFromShellUsesMarkedOutputEvenWhenShellExitsNonzero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not applicable on Windows")
	}

	dir := t.TempDir()
	shell := filepath.Join(dir, "fake-shell")
	script := "#!/bin/sh\nprintf 'noise __CLAWMETER_PATH__/tmp/codex/bin:/usr/bin__CLAWMETER_PATH__'\nexit 7\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}

	got := capturePathFromShell(shell)
	if len(got) != 2 || got[0] != "/tmp/codex/bin" || got[1] != "/usr/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want marked PATH despite nonzero exit", got)
	}
}

func TestLoginShellFromPasswdMatchesUIDOrUsername(t *testing.T) {
	passwd := strings.Join([]string{
		"root:x:0:0:root:/root:/bin/bash",
		"tnunamak:x:1000:1000:Tim:/home/tnunamak:/usr/bin/zsh",
	}, "\n")

	if got := loginShellFromPasswd("1000", "ignored", passwd); got != "/usr/bin/zsh" {
		t.Fatalf("loginShellFromPasswd by uid = %q", got)
	}
	if got := loginShellFromPasswd("9999", "domain\\tnunamak", passwd); got != "/usr/bin/zsh" {
		t.Fatalf("loginShellFromPasswd by username = %q", got)
	}
}

func TestExistingUniqueShellsFiltersMissingDuplicatesAndNonExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not applicable on Windows")
	}

	dir := t.TempDir()
	executable := filepath.Join(dir, "zsh")
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.WriteFile(plain, []byte("nope"), 0o644); err != nil {
		t.Fatalf("write plain file: %v", err)
	}

	got := existingUniqueShells([]string{"", executable, executable, plain, filepath.Join(dir, "missing")})
	if len(got) != 1 || got[0] != executable {
		t.Fatalf("existingUniqueShells() = %#v, want only executable once", got)
	}
}
