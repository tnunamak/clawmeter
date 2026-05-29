package openai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// makeFakeCodex puts an executable named "codex" on a fresh PATH so
// exec.LookPath finds it without inheriting whatever the host has installed.
func makeFakeCodex(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "codex")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", dir)
}

func TestSetupStatus(t *testing.T) {
	cases := []struct {
		name      string
		fakeCLI   bool
		auth      string // file contents; empty string means do not create file
		wantState provider.SetupState
		wantSub   string // substring expected in Detail
	}{
		{
			name:      "no codex CLI on PATH",
			fakeCLI:   false,
			wantState: provider.SetupUnavailable,
			wantSub:   "not installed",
		},
		{
			name:      "CLI present, no auth file",
			fakeCLI:   true,
			wantState: provider.SetupNeedsAuth,
			wantSub:   "codex login",
		},
		{
			name:      "CLI present, auth file with OPENAI_API_KEY",
			fakeCLI:   true,
			auth:      `{"OPENAI_API_KEY":"sk-test"}`,
			wantState: provider.SetupReady,
			wantSub:   "API key",
		},
		{
			name:      "CLI present, auth file with oauth tokens",
			fakeCLI:   true,
			auth:      `{"tokens":{"access_token":"at","refresh_token":"rt"}}`,
			wantState: provider.SetupReady,
			wantSub:   "ChatGPT",
		},
		{
			name:      "CLI present, auth file empty tokens",
			fakeCLI:   true,
			auth:      `{"tokens":{"access_token":"","refresh_token":""}}`,
			wantState: provider.SetupNeedsAuth,
			wantSub:   "no credentials",
		},
		{
			name:      "CLI present, malformed auth file",
			fakeCLI:   true,
			auth:      `not json`,
			wantState: provider.SetupNeedsAuth,
			wantSub:   "unreadable",
		},
		{
			name:      "CLI present, OPENAI_API_KEY whitespace only",
			fakeCLI:   true,
			auth:      `{"OPENAI_API_KEY":"   "}`,
			wantState: provider.SetupNeedsAuth,
			wantSub:   "no credentials",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldInitShellPath := initShellPath
			initShellPath = func() {}
			t.Cleanup(func() { initShellPath = oldInitShellPath })

			if tc.fakeCLI {
				makeFakeCodex(t)
			} else {
				// guarantee codex is not findable
				t.Setenv("PATH", t.TempDir())
			}

			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			if tc.auth != "" {
				if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(tc.auth), 0o600); err != nil {
					t.Fatalf("write auth.json: %v", err)
				}
			}

			st := New(config.ProviderConfig{}).SetupStatus()
			if st.State != tc.wantState {
				t.Fatalf("state = %q, want %q (detail=%q)", st.State, tc.wantState, st.Detail)
			}
			if tc.wantSub != "" && !strings.Contains(st.Detail, tc.wantSub) {
				t.Fatalf("detail = %q, want substring %q", st.Detail, tc.wantSub)
			}
		})
	}
}

func TestCodexExecutablePathRecoversShellPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "codex")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	oldInitShellPath := initShellPath
	initShellPath = func() {
		t.Setenv("PATH", dir)
	}
	t.Cleanup(func() { initShellPath = oldInitShellPath })

	got, err := codexExecutablePath()
	if err != nil {
		t.Fatalf("codexExecutablePath() error = %v", err)
	}
	if got != exe {
		t.Fatalf("codexExecutablePath() = %q, want %q", got, exe)
	}
}

func TestIsConfiguredMatchesSetupStatus(t *testing.T) {
	oldInitShellPath := initShellPath
	initShellPath = func() {}
	t.Cleanup(func() { initShellPath = oldInitShellPath })

	makeFakeCodex(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	p := New(config.ProviderConfig{})
	if p.IsConfigured() {
		t.Fatalf("IsConfigured() = true with no auth file, want false")
	}

	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-x"}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	if !p.IsConfigured() {
		t.Fatalf("IsConfigured() = false with valid API key, want true")
	}
}
