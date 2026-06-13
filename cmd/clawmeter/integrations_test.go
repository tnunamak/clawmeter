package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	result := setupClaudeStatuslineIntegration(false)
	if result.Status != "installed" {
		t.Fatalf("expected installed, got %#v", result)
	}

	path := filepath.Join(home, ".claude", "settings.local.json")
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
