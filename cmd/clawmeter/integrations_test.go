package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTmuxStatusRightWithClawmeter_PrependsAndIsIdempotent(t *testing.T) {
	existing := "#(/home/user/custom.sh) #[fg=white]%H:%M"
	next, changed := tmuxStatusRightWithClawmeter(existing)
	if !changed {
		t.Fatal("expected first install to change status-right")
	}
	if !strings.Contains(next, "#("+clawmeterStatuslineCommand+")") {
		t.Fatalf("missing clawmeter segment: %s", next)
	}
	if !strings.HasSuffix(next, existing) {
		t.Fatalf("did not preserve existing status-right suffix: %s", next)
	}

	again, changed := tmuxStatusRightWithClawmeter(next)
	if changed {
		t.Fatal("expected second install to be idempotent")
	}
	if again != next {
		t.Fatalf("idempotent call changed status-right:\n%s\n%s", next, again)
	}
}

func TestMergeClaudeStatusLine_CreatesSettings(t *testing.T) {
	out, changed, err := mergeClaudeStatusLine(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected empty settings to change")
	}

	var settings map[string]any
	if err := json.Unmarshal(out, &settings); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	statusLine, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("missing statusLine: %#v", settings)
	}
	if statusLine["command"] != clawmeterStatuslineCommand {
		t.Fatalf("wrong statusline command: %#v", statusLine)
	}
}

func TestMergeClaudeStatusLine_PreservesExistingSettings(t *testing.T) {
	input := []byte(`{"permissions":{"allow":["Bash(go test ./...)"]},"model":"sonnet"}`)
	out, changed, err := mergeClaudeStatusLine(input)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected missing statusLine to change")
	}

	var settings map[string]any
	if err := json.Unmarshal(out, &settings); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if settings["model"] != "sonnet" {
		t.Fatalf("model setting was not preserved: %#v", settings)
	}
	if _, ok := settings["permissions"].(map[string]any); !ok {
		t.Fatalf("permissions setting was not preserved: %#v", settings)
	}
}

func TestMergeClaudeStatusLine_IsIdempotent(t *testing.T) {
	input := []byte(`{"statusLine":{"type":"command","command":"clawmeter statusline"},"theme":"dark"}` + "\n")
	out, changed, err := mergeClaudeStatusLine(input)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected already-installed settings to be unchanged")
	}
	if string(out) != string(input) {
		t.Fatalf("idempotent merge changed bytes:\n%s\n%s", input, out)
	}
}

func TestSetupClaudeStatuslineIntegration_WritesIsolatedHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	volume := filepath.VolumeName(home)
	t.Setenv("HOMEDRIVE", volume)
	t.Setenv("HOMEPATH", strings.TrimPrefix(home, volume))

	result := setupClaudeStatuslineIntegration(false)
	if result.Status != "installed" {
		t.Fatalf("expected installed, got %#v", result)
	}

	path, err := claudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), clawmeterStatuslineCommand) {
		t.Fatalf("settings missing command: %s", data)
	}

	result = setupClaudeStatuslineIntegration(false)
	if result.Status != "ok" {
		t.Fatalf("expected idempotent ok, got %#v", result)
	}
}

func integrationTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	volume := filepath.VolumeName(home)
	t.Setenv("HOMEDRIVE", volume)
	t.Setenv("HOMEPATH", strings.TrimPrefix(home, volume))
	return home
}

func TestClaudeSettingsUserLevelAndLegacyNotice(t *testing.T) {
	home := integrationTestHome(t)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "settings.local.json")
	data := []byte(`{"statusLine":{"type":"command","command":"clawmeter statusline"}}`)
	if err := os.WriteFile(legacy, data, 0o600); err != nil {
		t.Fatal(err)
	}
	result := setupClaudeStatuslineIntegration(false)
	if result.Status != "installed" {
		t.Errorf("expected user-level install, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Errorf("missing user-level settings.json: %v", err)
	}
	got, err := os.ReadFile(legacy)
	if err != nil || string(got) != string(data) {
		t.Errorf("legacy file changed: %q, %v", got, err)
	}
	if !strings.Contains(result.Detail, "settings.local.json") {
		t.Errorf("missing legacy notice: %#v", result)
	}
}

func TestClaudeNullSettings(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("null settings must not panic: %v", r)
		}
	}()
	out, changed, err := mergeClaudeStatusLine([]byte("null"))
	if err != nil || !changed || !json.Valid(out) {
		t.Fatalf("null should become object: %q, %v, %v", out, changed, err)
	}
}

func TestClaudeCustomStatuslinePreserved(t *testing.T) {
	for _, input := range []string{
		`{"statusLine":{"type":"command","command":"my-custom-status"}}`,
		`{"statusLine":"custom"}`,
		`{"statusLine":null}`,
	} {
		t.Run(input, func(t *testing.T) {
			out, changed, err := mergeClaudeStatusLine([]byte(input))
			if err != nil || changed || string(out) != input {
				t.Errorf("custom statusLine changed: %q, %v, %v", out, changed, err)
			}
		})
	}
}

func TestClaudeCustomStatuslineOutput(t *testing.T) {
	integrationTestHome(t)
	path, err := claudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"statusLine":{"type":"command","command":"custom"}}`)
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	result := setupClaudeStatuslineIntegration(false)
	if result.Changed || !strings.Contains(result.Detail, "preserved") {
		t.Errorf("custom statusline must be reported preserved: %#v", result)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(input) {
		t.Errorf("custom settings overwritten: %s", got)
	}
	if result := claudeStatuslineStatus(); result.Status == "installed" {
		t.Errorf("custom command reported installed: %#v", result)
	}
}

func TestClaudeBackupFailureAborts(t *testing.T) {
	integrationTestHome(t)
	path, err := claudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"theme":"dark"}`)
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	// Block every timestamp the current backup implementation can choose during this test.
	now := time.Now()
	for i := -5; i <= 120; i++ {
		backup := path + ".before-clawmeter-statusline." + now.Add(time.Duration(i)*time.Second).Format("20060102150405")
		if err := os.Mkdir(backup, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	result := setupClaudeStatuslineIntegration(false)
	if result.Status != "error" || result.Changed {
		t.Errorf("backup failure must abort: %#v", result)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(input) {
		t.Errorf("settings changed despite failed backup: %s", got)
	}
}

func TestClaudeSettingsAtomicReplacement(t *testing.T) {
	integrationTestHome(t)
	path, err := claudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"theme":"dark"}`)
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	link := path + ".original"
	if err := os.Link(path, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := os.WriteFile(path+".tmp", []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := setupClaudeStatuslineIntegration(false)
	if result.Status != "installed" {
		t.Fatalf("install failed: %#v", result)
	}
	got, _ := os.ReadFile(link)
	if string(got) != string(input) {
		t.Errorf("original inode was truncated instead of atomic replacement: %s", got)
	}
	got, _ = os.ReadFile(path + ".tmp")
	if string(got) != "sentinel" {
		t.Errorf("fixed temporary path was overwritten: %s", got)
	}
}

func TestClaudeForceReplacesCustomStatusline(t *testing.T) {
	setup, ok := any(setupClaudeStatuslineIntegration).(func(bool, ...bool) integrationResult)
	if !ok {
		t.Fatal("setup must accept an explicit force option")
	}
	integrationTestHome(t)
	path, err := claudeSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"statusLine":{"type":"command","command":"custom"},"theme":"dark"}`)
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := setup(true, true); got.Status != "would change" {
		t.Fatalf("force preview: %#v", got)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(input) {
		t.Fatal("dry run changed settings")
	}
	result := setup(false, true)
	if result.Status != "installed" || !result.Changed {
		t.Fatalf("force did not install: %#v", result)
	}
	got, _ = os.ReadFile(path)
	if !strings.Contains(string(got), clawmeterStatuslineCommand) || !strings.Contains(string(got), "dark") {
		t.Errorf("bad forced settings: %s", got)
	}
	backups, err := filepath.Glob(path + ".before-clawmeter-statusline.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("missing backup: %v, %v", backups, err)
	}
	got, _ = os.ReadFile(backups[0])
	if string(got) != string(input) {
		t.Fatal("backup did not preserve original")
	}
}
