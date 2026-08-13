//go:build !windows

package shellpath

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const shellPathSubprocessTest = "CLAWMETER_SHELLPATH_SUBPROCESS_TEST"

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

func TestMissingEnvNamesFiltersInvalidPresentAndDuplicateNames(t *testing.T) {
	t.Setenv("CLAWMETER_PRESENT", "value")
	got := missingEnvNames([]string{"CLAWMETER_PRESENT", "CLAWMETER_MISSING", "CLAWMETER_MISSING", "bad-name"})
	if !reflect.DeepEqual(got, []string{"CLAWMETER_MISSING"}) {
		t.Fatalf("missingEnvNames() = %#v, want only the absent valid name", got)
	}
}

func TestParseMarkedEnvReturnsOnlyRequestedValues(t *testing.T) {
	out := []byte("noise\x00" + envMarker + "REQUESTED\x00test-value\x00" + envMarker + "UNREQUESTED\x00other" + envMarker + "test-value")
	got := parseMarkedEnv(out, []string{"REQUESTED"})
	if got["REQUESTED"] != "test-value" || len(got) != 1 {
		t.Fatalf("parseMarkedEnv() returned unexpected requested-value presence")
	}
}

func TestSessionEnvironmentResolverCachesValuesAndMissesByNameSet(t *testing.T) {
	calls := 0
	resolver := newSessionEnvironmentResolver(func(request provider.SessionEnvironmentRequest) map[string]string {
		calls++
		return map[string]string{"CLAWMETER_REQUESTED": "cached"}
	})
	first := resolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: []string{"CLAWMETER_MISSING", "CLAWMETER_REQUESTED"}, AllowSessionEnvironmentFallback: true})
	second := resolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: []string{"CLAWMETER_REQUESTED", "CLAWMETER_MISSING"}, AllowSessionEnvironmentFallback: true})
	if calls != 1 || first["CLAWMETER_REQUESTED"] != "cached" || second["CLAWMETER_REQUESTED"] != "cached" {
		t.Fatalf("calls = %d, values = %#v / %#v; want one cached resolution", calls, first, second)
	}
	if _, ok := second["CLAWMETER_MISSING"]; ok {
		t.Fatal("cached miss unexpectedly became a value")
	}
}

func TestSessionEnvironmentResolverReobservesCredentialsAfterTTL(t *testing.T) {
	now := time.Unix(100, 0)
	credential := "account-a"
	calls := 0
	resolver := newSessionEnvironmentResolver(func(request provider.SessionEnvironmentRequest) map[string]string {
		calls++
		return map[string]string{"CLAWMETER_REQUESTED": credential}
	}).(*sessionEnvironmentResolver)
	resolver.now = func() time.Time { return now }
	request := provider.SessionEnvironmentRequest{EnvNames: []string{"CLAWMETER_REQUESTED"}, AllowSessionEnvironmentFallback: true}
	if got := resolver.ResolveSessionEnvironment(request)["CLAWMETER_REQUESTED"]; got != "account-a" {
		t.Fatalf("first credential = %q", got)
	}
	credential = "account-b"
	if got := resolver.ResolveSessionEnvironment(request)["CLAWMETER_REQUESTED"]; got != "account-a" || calls != 1 {
		t.Fatalf("unexpired snapshot = %q, calls=%d", got, calls)
	}
	now = now.Add(sessionEnvironmentCacheTTL)
	if got := resolver.ResolveSessionEnvironment(request)["CLAWMETER_REQUESTED"]; got != "account-b" || calls != 2 {
		t.Fatalf("refreshed credential = %q, calls=%d", got, calls)
	}
}

func TestSessionEnvironmentResolverCachesValuesAndMisses(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "zsh")
	count := filepath.Join(dir, "count")
	script := "#!/bin/sh\nprintf x >> '" + count + "'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("CLAWMETER_REQUESTED", "")
	t.Setenv("CLAWMETER_MISSING", "")
	resolver := NewSessionEnvironmentResolver()
	request := provider.SessionEnvironmentRequest{EnvNames: []string{"CLAWMETER_MISSING", "CLAWMETER_REQUESTED"}, AllowSessionEnvironmentFallback: true}
	first := resolver.ResolveSessionEnvironment(request)
	second := resolver.ResolveSessionEnvironment(request)
	if len(first) != 0 || len(second) != 0 {
		t.Fatalf("resolver values = %#v, %#v; want cached misses", first, second)
	}
	if got, err := os.ReadFile(count); err != nil || string(got) != "x" {
		t.Fatalf("shell probe count = %q, %v; want exactly one probe", got, err)
	}
	if os.Getenv("CLAWMETER_REQUESTED") != "" {
		t.Fatal("resolver leaked a recovered value into the process environment")
	}
}

func TestSessionEnvironmentResolverIsLazyAndImportsOnlyRequestedValues(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	if os.Getenv(shellPathSubprocessTest) == "1" {
		sessionEnvironmentResolverChild(t)
		return
	}

	zdotdir := t.TempDir()
	countFile := filepath.Join(t.TempDir(), "count")
	if err := os.WriteFile(filepath.Join(zdotdir, ".zshrc"), []byte("printf x >> \"$CLAWMETER_TEST_COUNT\"\nexport CLAWMETER_REQUESTED_SECRET='test-value'\nexport CLAWMETER_UNREQUESTED_SECRET='must-not-import'\n"), 0o600); err != nil {
		t.Fatalf("write isolated .zshrc: %v", err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionEnvironmentResolverIsLazyAndImportsOnlyRequestedValues$")
	cmd.Stdin = devNull
	cmd.Env = append(os.Environ(), shellPathSubprocessTest+"=1", "SHELL="+zsh, "ZDOTDIR="+zdotdir, "CLAWMETER_TEST_COUNT="+countFile, "CLAWMETER_REQUESTED_SECRET=", "CLAWMETER_UNREQUESTED_SECRET=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, output)
	}
	if got := string(output); !strings.HasPrefix(got, "before=false\nrecovered=true\nglobal=false\nunrequested=false\n") {
		t.Fatalf("subprocess output = %q, want lazy presence flags only", got)
	}
}

func sessionEnvironmentResolverChild(t *testing.T) {
	t.Helper()
	_, beforeErr := os.Stat(os.Getenv("CLAWMETER_TEST_COUNT"))
	resolver := NewSessionEnvironmentResolver()
	recovered := resolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
		EnvNames:                        []string{"CLAWMETER_REQUESTED_SECRET"},
		AllowSessionEnvironmentFallback: true,
	})
	_, requested := recovered["CLAWMETER_REQUESTED_SECRET"]
	fmt.Fprintf(os.Stdout, "before=%t\nrecovered=%t\nglobal=%t\nunrequested=%t\n", beforeErr == nil, requested, os.Getenv("CLAWMETER_REQUESTED_SECRET") != "", os.Getenv("CLAWMETER_UNREQUESTED_SECRET") != "")
}

func TestCapturePathFromShellUsesMarkedOutputEvenWhenShellExitsNonzero(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "sh")
	script := "#!/bin/sh\nprintf 'noise __CLAWMETER_PATH__/tmp/codex/bin:/usr/bin__CLAWMETER_PATH__'\nexit 7\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}

	got := capturePathFromShellWithPolicy(shell, true, runShellCommand)
	if len(got) != 2 || got[0] != "/tmp/codex/bin" || got[1] != "/usr/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want marked PATH despite nonzero exit", got)
	}
}

func TestCapturePathFromShellSkipsInteractiveZshWithoutTerminal(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "zsh")
	count := filepath.Join(dir, "count")
	script := "#!/bin/sh\nprintf x >> '" + count + "'\nif [ \"$2\" = \"-i\" ]; then exit 7; fi\nsleep 0.05\nprintf '__CLAWMETER_PATH__/home/user/.nvm/versions/node/v22/bin:/usr/bin__CLAWMETER_PATH__'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}

	got := capturePathFromShellWithPolicy(shell, false, runShellCommand)
	if len(got) != 2 || got[0] != "/home/user/.nvm/versions/node/v22/bin" || got[1] != "/usr/bin" {
		t.Fatalf("capturePathFromShell() = %#v, want recovered PATH", got)
	}
	if data, err := os.ReadFile(count); err != nil || string(data) != "x" {
		t.Fatalf("shell invocation count = %q, %v; want recovery only", data, err)
	}
}

func TestCapturePathFromShellNoTTYSlowRecoverySubprocess(t *testing.T) {
	if os.Getenv(shellPathSubprocessTest) == "1" {
		runNoTTYSlowRecoveryChild(t)
		return
	}

	dir := t.TempDir()
	markerFile := filepath.Join(dir, "probe-marker")
	shell := filepath.Join(dir, "zsh")
	script := "#!/bin/sh\n" +
		"case \" $* \" in\n" +
		"  *\" -i \"*) printf interactive > \"$CLAWMETER_SHELLPATH_MARKER\"; exit 7 ;;\n" +
		"esac\n" +
		"printf recovery > \"$CLAWMETER_SHELLPATH_MARKER\"\n" +
		"sleep 4\n" +
		"printf '__CLAWMETER_PATH__/home/user/.nvm/versions/node/v22/bin:/usr/bin__CLAWMETER_PATH__'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake zsh: %v", err)
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestCapturePathFromShellNoTTYSlowRecoverySubprocess$")
	cmd.Stdin = devNull
	cmd.Env = append(os.Environ(),
		shellPathSubprocessTest+"=1",
		"CLAWMETER_SHELLPATH_SHELL="+shell,
		"CLAWMETER_SHELLPATH_MARKER="+markerFile,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, output)
	}

	if got := string(output); !strings.Contains(got, "terminal=false") || !strings.Contains(got, "path=/home/user/.nvm/versions/node/v22/bin:/usr/bin") {
		t.Fatalf("subprocess output = %q, want terminal=false and recovered PATH", got)
	}
	if got, err := os.ReadFile(markerFile); err != nil || string(got) != "recovery" {
		t.Fatalf("probe marker = %q, %v; want recovery only", got, err)
	}
}

func runNoTTYSlowRecoveryChild(t *testing.T) {
	t.Helper()
	if terminalAvailable() {
		t.Fatalf("terminalAvailable() = true with child stdin connected to %s", os.DevNull)
	}

	path := capturePathFromShell(os.Getenv("CLAWMETER_SHELLPATH_SHELL"))
	fmt.Fprintf(os.Stdout, "terminal=%t\npath=%s\n", terminalAvailable(), strings.Join(path, string(os.PathListSeparator)))
}

func TestCapturePathFromShellAllowsSlowZshRecoveryWithinRecoveryBound(t *testing.T) {
	var calls [][]string
	var timeouts []time.Duration
	run := func(_ context.Context, _ string, args []string, timeout time.Duration) ([]byte, error) {
		calls = append(calls, args)
		timeouts = append(timeouts, timeout)
		if timeout <= 3*time.Second {
			return nil, context.DeadlineExceeded
		}
		return []byte("__CLAWMETER_PATH__/slow/recovery/bin__CLAWMETER_PATH__"), nil
	}

	got := capturePathFromShellWithPolicy("/bin/zsh", false, run)
	if !reflect.DeepEqual(got, []string{"/slow/recovery/bin"}) {
		t.Fatalf("capturePathFromShell() = %#v, want slow recovery PATH", got)
	}
	if len(calls) != 1 || calls[0][1] != "-c" {
		t.Fatalf("zsh calls = %#v, want recovery only", calls)
	}
	if !reflect.DeepEqual(timeouts, []time.Duration{zshRecoveryTimeout}) {
		t.Fatalf("zsh timeouts = %#v, want [%s]", timeouts, zshRecoveryTimeout)
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

	got := capturePathFromShellWithPolicy(shell, true, runShellCommand)
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

	got := capturePathFromShellWithPolicy(shell, true, runShellCommand)
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

func TestSessionExecutableResolverFindsStandardUserBin(t *testing.T) {
	home := t.TempDir()
	name := "clawmeter-test-user-bin-tool"
	path := filepath.Join(home, ".local", "bin", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	got, err := NewSessionEnvironmentResolver().(provider.SessionExecutableResolver).ResolveSessionExecutable(name)
	if err != nil || got != path {
		t.Fatalf("ResolveSessionExecutable() = %q, %v; want %q", got, err, path)
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
		if probe.interactiveTimeout != captureTimeout {
			t.Fatalf("%s interactive timeout = %s, want %s", name, probe.interactiveTimeout, captureTimeout)
		}
	}
	zsh, _ := probeForShell("/bin/zsh")
	if zsh.recoveryTimeout != zshRecoveryTimeout {
		t.Fatalf("zsh recovery timeout = %s, want %s", zsh.recoveryTimeout, zshRecoveryTimeout)
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
