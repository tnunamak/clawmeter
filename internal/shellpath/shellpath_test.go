//go:build !windows

package shellpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMergeDeduplicates(t *testing.T) {
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

func TestCapturePathFromShellUsesMarkedOutputEvenWhenShellExitsNonzero(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "sh")
	script := "#!/bin/sh\nprintf 'noise __CLAWMETER_PATH__/tmp/codex/bin:/usr/bin__CLAWMETER_PATH__'\nexit 7\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}

	got := capturePathFromShell(shell)
	if len(got) != 2 || got[0] != "/tmp/codex/bin" || got[1] != "/usr/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want marked PATH despite nonzero exit", got)
	}
}

func TestCapturePathFromShellFallsBackToNonInteractiveZsh(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "zsh")
	count := filepath.Join(dir, "count")
	script := "#!/bin/sh\nprintf x >> '" + count + "'\nif [ \"$2\" = \"-i\" ]; then exit 7; fi\nprintf '__CLAWMETER_PATH__/home/user/.nvm/versions/node/v22/bin:/usr/bin__CLAWMETER_PATH__'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}

	got := capturePathFromShell(shell)
	if len(got) != 2 || got[0] != "/home/user/.nvm/versions/node/v22/bin" || got[1] != "/usr/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want fallback PATH", got)
	}
	if data, err := os.ReadFile(count); err != nil || string(data) != "xx" {
		t.Fatalf("shell invocation count = %q, %v; want interactive and fallback", data, err)
	}
}

func TestCapturePathFromShellDoesNotRunFallbackAfterSuccessfulCapture(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "zsh")
	count := filepath.Join(dir, "count")
	script := "#!/bin/sh\nprintf x >> '" + count + "'\nprintf '__CLAWMETER_PATH__/home/user/.nvm/bin:/usr/bin__CLAWMETER_PATH__'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}

	got := capturePathFromShell(shell)
	if len(got) != 2 || got[0] != "/home/user/.nvm/bin" || got[1] != "/usr/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want marked PATH", got)
	}
	if data, err := os.ReadFile(count); err != nil || string(data) != "x" {
		t.Fatalf("shell invocation count = %q, %v; want one attempt", data, err)
	}
}

func TestCapturePathFromShellRecoversFromRealZshStartupFailure(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}

	zdotdir := t.TempDir()
	t.Setenv("ZDOTDIR", zdotdir)
	zshrc := `if [[ -o interactive ]]; then
  exit 7
fi
export PATH="/home/user/.nvm/versions/node/v22/bin:$PATH"
`
	if err := os.WriteFile(filepath.Join(zdotdir, ".zshrc"), []byte(zshrc), 0o600); err != nil {
		t.Fatalf("write isolated .zshrc: %v", err)
	}

	got := capturePathFromShell(zsh)
	if len(got) == 0 || got[0] != "/home/user/.nvm/versions/node/v22/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want PATH recovered from .zshrc", got)
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

func TestLoginShellCandidatesUsesSHELLThenFallback(t *testing.T) {
	if got := loginShellCandidatesFrom(" /custom/fish ", "/usr/bin/bash"); !reflect.DeepEqual(got, []string{"/custom/fish", "/usr/bin/bash", "/bin/sh"}) {
		t.Fatalf("loginShellCandidatesFrom() = %#v", got)
	}
	if got := loginShellCandidatesFrom("", "/usr/bin/bash"); !reflect.DeepEqual(got, []string{"/usr/bin/bash", "/bin/sh"}) {
		t.Fatalf("loginShellCandidatesFrom() = %#v", got)
	}
	if got := loginShellCandidatesFrom("/bin/nu", "/usr/bin/bash"); !reflect.DeepEqual(got, []string{"/bin/nu", "/usr/bin/bash", "/bin/sh"}) {
		t.Fatalf("loginShellCandidatesFrom() = %#v", got)
	}
}

func TestAuthoritativeLoginShellResolvesThroughInheritedPATH(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "bash")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	t.Setenv("SHELL", "bash")
	t.Setenv("PATH", dir)

	if got := authoritativeLoginShell(); got != shell {
		t.Fatalf("authoritativeLoginShell() = %q, want %q", got, shell)
	}
}

func TestAuthoritativeLoginShellSkipsUnsupportedSHELL(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"nu", "bash"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write fake shell: %v", err)
		}
	}
	t.Setenv("PATH", dir)
	got := authoritativeLoginShellFrom(loginShellCandidatesFrom("nu", "bash"))
	if got != filepath.Join(dir, "bash") {
		t.Fatalf("authoritativeLoginShellFrom() = %q, want passwd shell", got)
	}
}

func TestCaptureMergeAndLookPathPreserveInheritedPrecedence(t *testing.T) {
	root := t.TempDir()
	inherited := filepath.Join(root, "inherited")
	captured := filepath.Join(root, "captured")
	if err := os.MkdirAll(inherited, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(captured, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(inherited, "same-name"), filepath.Join(captured, "captured-cli")} {
		if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shell := filepath.Join(root, "bash")
	script := "#!/bin/sh\nprintf '__CLAWMETER_PATH__" + captured + "__CLAWMETER_PATH__'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", inherited)
	pathFromShell := capturePathFromShell(shell)
	merge(pathFromShell)
	if got := os.Getenv("PATH"); got != inherited+string(os.PathListSeparator)+captured {
		t.Fatalf("merged PATH = %q", got)
	}
	if got, err := exec.LookPath("captured-cli"); err != nil || got != filepath.Join(captured, "captured-cli") {
		t.Fatalf("captured CLI lookup = %q, %v", got, err)
	}
	if got, err := exec.LookPath("same-name"); err != nil || got != filepath.Join(inherited, "same-name") {
		t.Fatalf("inherited CLI lookup = %q, %v", got, err)
	}
}

func TestProbeForShellUsesShellSpecificProtocol(t *testing.T) {
	for _, name := range []string{"zsh", "bash", "sh", "dash", "ksh"} {
		probe, ok := probeForShell("/bin/" + name)
		if !ok {
			t.Fatalf("probeForShell(%q) not supported", name)
		}
		args := probe.loginInteractiveArgs
		if len(args) != 4 || args[0] != "-l" || args[1] != "-i" || args[2] != "-c" || !strings.Contains(args[3], "$PATH") {
			t.Fatalf("%s probe args = %#v", name, args)
		}
	}

	fish, ok := probeForShell("/usr/bin/fish")
	if !ok || !strings.Contains(fish.loginInteractiveArgs[3], "string join : -- $PATH") {
		t.Fatalf("fish probe = %#v, want fish list join", fish)
	}
}

func TestUnsupportedShellFailsSoftWithoutProbe(t *testing.T) {
	if _, ok := probeForShell("/bin/nu"); ok {
		t.Fatal("nushell unexpectedly accepted")
	}
	if got := capturePathFromShell("/bin/nu"); got != nil {
		t.Fatalf("capturePathFromShell(nu) = %#v, want nil", got)
	}
}
