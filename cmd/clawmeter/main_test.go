package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"runtime"
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

func configPathForHome(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "clawmeter", "config.yaml")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "clawmeter", "config.yaml")
	default:
		return filepath.Join(home, ".config", "clawmeter", "config.yaml")
	}
}

// runWithHome isolates config, credentials, and executable discovery. Do not
// inherit the host environment: provider tokens and config overrides live there.
func runWithHome(t *testing.T, bin, home string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = home
	volume := filepath.VolumeName(home)
	cmd.Env = []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"HOMEDRIVE=" + volume,
		"HOMEPATH=" + strings.TrimPrefix(home, volume),
		"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_RUNTIME_DIR=" + home,
		"TMPDIR=" + home,
		"TMP=" + home,
		"TEMP=" + home,
		"PATH=" + home,
	}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
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
	cfgPath := configPathForHome(home)
	if data, err := os.ReadFile(cfgPath); err == nil {
		if strings.Contains(string(data), "opneai") {
			t.Errorf("config.yaml contains the typo'd name: %s", data)
		}
	}
}

func TestConfigSetPollIntervalRejectsBelowSafeFloor(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	_, stderr, code := runWithHome(t, bin, home, "config", "set", "poll_interval", "299")
	if code == 0 {
		t.Fatal("poll_interval below five minutes was accepted")
	}
	if !strings.Contains(stderr, "must be >= 300 seconds") {
		t.Fatalf("stderr = %q, want five-minute floor", stderr)
	}

	_, stderr, code = runWithHome(t, bin, home, "config", "set", "poll_interval", "300")
	if code != 0 {
		t.Fatalf("five-minute poll interval rejected: %s", stderr)
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

func TestConfigEnable_AcceptsProviderAlias(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	stdout, stderr, code := runWithHome(t, bin, home, "config", "enable", "grok")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Enabled provider: xai") {
		t.Errorf("expected canonical provider confirmation, got: %s", stdout)
	}
	cfgPath := configPathForHome(home)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "xai:") {
		t.Fatalf("config should contain canonical xai key: %s", data)
	}
	if strings.Contains(string(data), "grok:") {
		t.Fatalf("config should not persist alias key: %s", data)
	}
}

func TestProvidersEnable_AliasesConfigEnable(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	stdout, stderr, code := runWithHome(t, bin, home, "providers", "enable", "codex")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Enabled provider: openai") {
		t.Errorf("expected canonical provider confirmation, got: %s", stdout)
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

func TestSourceCommandsCanonicalizeAndRoundTrip(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	nativeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nativeDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"native-test","expiresAt":4102444800000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "work-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "Claude", "Work", "config-dir", profile, "--label", "Work")
	if code != 0 || !strings.Contains(stdout, "Restart a running tray") {
		t.Fatalf("source add: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	data, err := os.ReadFile(configPathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: work") || !strings.Contains(string(data), filepath.Clean(profile)) {
		t.Fatalf("source config was not canonical: %s", data)
	}
	if !strings.Contains(string(data), "id: default") || !strings.Contains(string(data), "kind: native") {
		t.Fatalf("source transition did not preserve the native default: %s", data)
	}
	stdout, stderr, code = runWithHome(t, bin, home, "providers", "source", "list", "claude")
	if code != 0 || !strings.Contains(stdout, "claude\twork\tWork") {
		t.Fatalf("source list: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runWithHome(t, bin, home, "providers", "source", "remove", "Claude", "WORK")
	if code != 0 || !strings.Contains(stdout, "Restart a running tray") {
		t.Fatalf("source remove: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runWithHome(t, bin, home, "providers", "source", "remove", "claude", "default")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "disabled the provider") {
		t.Fatalf("remove final source: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	data, err = os.ReadFile(configPathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sources:") || !strings.Contains(string(data), "enabled: false") {
		t.Fatalf("final source removal left enrollment or polling enabled: %s", data)
	}
}

func TestSourceHelpAndUnsupportedKindAreGeneric(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	stdout, stderr, code := runWithHome(t, bin, home, "providers", "source", "help", "claude")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Supported source kinds for claude:") || !strings.Contains(stdout, "config-dir") {
		t.Fatalf("source help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, stderr, code = runWithHome(t, bin, home, "providers", "source", "add", "zai", "work", "slot", "work")
	if code == 0 || !strings.Contains(stderr, `provider "zai" does not support source kind "slot"`) || !strings.Contains(stderr, "Supported source kinds for zai:") {
		t.Fatalf("unsupported source kind: code=%d stderr=%q", code, stderr)
	}
}

func TestSourceAddDoesNotInventUnavailableDefault(t *testing.T) {
	bin := buildBinary(t)
	// Simulate an authenticated developer without reading real credentials.
	credentialsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentialsDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"host-test","expiresAt":4102444800000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "host-test")
	t.Setenv("CLAUDE_CONFIG_DIR", credentialsDir)
	home := t.TempDir()
	profile := filepath.Join(home, "work-profile")
	if _, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "claude", "work", "config-dir", profile); code != 0 {
		t.Fatalf("source add: code=%d stderr=%q", code, stderr)
	}
	data, err := os.ReadFile(configPathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "id: default") {
		t.Fatalf("unavailable native source was invented: %s", data)
	}
}

func TestSourceCommandWithoutSubcommandListsSafely(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	profile := filepath.Join(home, "profile")
	if _, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "claude", "work", "config-dir", profile); code != 0 {
		t.Fatalf("source add: code=%d stderr=%q", code, stderr)
	}
	withoutSubcommand, stderr, code := runWithHome(t, bin, home, "providers", "source")
	if code != 0 || stderr != "" {
		t.Fatalf("source without subcommand: code=%d stdout=%q stderr=%q", code, withoutSubcommand, stderr)
	}
	withList, stderr, code := runWithHome(t, bin, home, "providers", "source", "list")
	if code != 0 || stderr != "" || withoutSubcommand != withList {
		t.Fatalf("source list mismatch: no-subcommand=%q list=%q stderr=%q", withoutSubcommand, withList, stderr)
	}
}

func TestSourceAddUsesProviderDeclaredReferenceRules(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	stdout, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "claude", "default", "native", "--label", "Personal")
	if code != 0 {
		t.Fatalf("native source: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	data, err := os.ReadFile(configPathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kind: native") || strings.Contains(string(data), "ref:") {
		t.Fatalf("native source reference was not omitted: %s", data)
	}

	home = t.TempDir()
	_, stderr, code = runWithHome(t, bin, home, "providers", "source", "add", "claude", "default", "config-dir")
	if code == 0 || !strings.Contains(stderr, "requires a reference") {
		t.Fatalf("missing config-dir ref: code=%d stderr=%q", code, stderr)
	}

	home = t.TempDir()
	_, stderr, code = runWithHome(t, bin, home, "providers", "source", "add", "claude", "default", "native", "unexpected")
	if code == 0 || !strings.Contains(stderr, "does not accept a reference") {
		t.Fatalf("unexpected native ref: code=%d stderr=%q", code, stderr)
	}
}

func TestSourceAddCanExplicitlyEnrollDefaultWithoutDuplication(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	profile := filepath.Join(home, "personal-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "claude", "default", "config-dir", profile, "--label", "Personal")
	if code != 0 {
		t.Fatalf("source add default: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	data, err := os.ReadFile(configPathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "id: default") != 1 || !strings.Contains(string(data), "label: Personal") || !strings.Contains(string(data), "kind: config-dir") {
		t.Fatalf("explicit default source was not stored exactly once: %s", data)
	}
}

func TestSourceCommandShowsEnrolledSourceWhenCredentialsBecomeUnavailable(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	missingProfile := filepath.Join(home, "missing-claude-profile")

	if _, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "claude", "work", "config-dir", missingProfile, "--label", "Work"); code != 0 {
		t.Fatalf("add source: exit %d: %s", code, stderr)
	}

	stdout, stderr, code := runWithHome(t, bin, home, "claude", "--source", "work", "--plain")
	if code != 1 {
		t.Fatalf("unavailable enrolled source exit = %d, want fetch-error exit 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "is not configured") {
		t.Fatalf("enrolled source was rejected before rendering its source-local error: %s", stderr)
	}
	if !strings.Contains(stdout, "Claude") || !strings.Contains(stdout, "Work") {
		t.Fatalf("source-local output missing enrolled source identity: %q", stdout)
	}
}

func TestSourceAddExpandsBothTildeSeparatorStyles(t *testing.T) {
	bin := buildBinary(t)
	for _, input := range []string{"~/profile", `~\profile`} {
		home := t.TempDir()
		_, stderr, code := runWithHome(t, bin, home, "providers", "source", "add", "claude", "work", "config-dir", input)
		if code != 0 {
			t.Fatalf("source add %q: code=%d stderr=%q", input, code, stderr)
		}
		data, err := os.ReadFile(configPathForHome(home))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), filepath.Join(home, "profile")) {
			t.Fatalf("source add %q did not expand under home: %s", input, data)
		}
	}
}

func TestSourceIsTopLevelStatusShortcut(t *testing.T) {
	if !isStatusShortcutFlag("--source") || !isStatusShortcutFlag("-source") {
		t.Fatal("source flag should route through the top-level status parser")
	}
}

func TestProvidersDiagnose_FailsClosedOnInvalidConfig(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	configDir := filepath.Dir(configPathForHome(home))
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("providers: [invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runWithHome(t, bin, home, "providers", "diagnose", "all", "--json")
	if code == 0 {
		t.Fatalf("expected invalid config to fail, stdout: %s", stdout)
	}
	if stdout != "" {
		t.Fatalf("invalid config must not produce valid-looking diagnostic JSON: %s", stdout)
	}
	if !strings.Contains(stderr, "load config") {
		t.Fatalf("stderr = %q, want config error", stderr)
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

func buildInfoWith(version string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: version}}, true
	}
}

func TestResolveVersionKeepsLdflagsStampedRelease(t *testing.T) {
	// A release build stamps -X main.Version=<tag>. The embedded module version
	// must never override it, or a retagged build would report the wrong thing.
	if got := resolveVersion("v9.9.9", buildInfoWith("v0.34.4")); got != "v9.9.9" {
		t.Errorf("resolveVersion(stamped) = %q, want %q", got, "v9.9.9")
	}
}

func TestResolveVersionFallsBackToModuleVersion(t *testing.T) {
	// `go install ...@latest` passes no ldflags, so the placeholder survives even
	// though the binary IS a tagged release. Reporting "dev" there made
	// `clawmeter update` refuse to run and made external updaters reinstall on
	// every invocation.
	if got := resolveVersion("dev", buildInfoWith("v0.34.4")); got != "v0.34.4" {
		t.Errorf("resolveVersion(go install) = %q, want %q", got, "v0.34.4")
	}
}

func TestResolveVersionStaysDevForNonReleaseBuilds(t *testing.T) {
	// "(devel)" is a working-tree build and "" means no module version was
	// recorded. Neither is a release, so self-update must stay disabled.
	for _, embedded := range []string{"(devel)", ""} {
		if got := resolveVersion("dev", buildInfoWith(embedded)); got != "dev" {
			t.Errorf("resolveVersion(dev, %q) = %q, want %q", embedded, got, "dev")
		}
	}
	unavailable := func() (*debug.BuildInfo, bool) { return nil, false }
	if got := resolveVersion("dev", unavailable); got != "dev" {
		t.Errorf("resolveVersion with no build info = %q, want %q", got, "dev")
	}
}

func TestReleaseVersionOrDevAcceptsOnlyReleaseTags(t *testing.T) {
	releases := []string{"v0.34.4", "v1.0.0", "v2.10.3-rc.1"}
	for _, v := range releases {
		if got := releaseVersionOrDev(v); got != v {
			t.Errorf("releaseVersionOrDev(%q) = %q, want %q", v, got, v)
		}
	}

	// A dirty local build of this repo reports a pseudo-version with +dirty, NOT
	// "(devel)". Treating it as a release would re-enable self-update for an
	// uncommitted tree and report a version that was never published.
	notReleases := []string{
		"",
		"(devel)",
		"v0.34.2-0.20260910012458-c0abf48acfd4",
		"v0.34.2-0.20260910012458-c0abf48acfd4+dirty",
		"v0.34.4+dirty",
		"devel",
		"0.34.4",
		"v1.2",
	}
	for _, v := range notReleases {
		if got := releaseVersionOrDev(v); got != defaultVersion {
			t.Errorf("releaseVersionOrDev(%q) = %q, want %q", v, got, defaultVersion)
		}
	}
}
