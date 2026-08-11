package alibabatoken

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func isolateConsoleHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func writeConsoleSession(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".bailian")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"access_token":"test-session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConnectReusesExistingSessionWithoutLookingUpCLI(t *testing.T) {
	home := isolateConsoleHome(t)
	writeConsoleSession(t, home)
	var stdout bytes.Buffer
	lookupCalled := false
	err := connect(context.Background(), false, &stdout, nil, func(string) (string, error) {
		lookupCalled = true
		return "", errors.New("must not look up CLI")
	}, func(context.Context, string, io.Writer, io.Writer) error {
		return errors.New("must not run CLI")
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookupCalled {
		t.Fatal("Connect looked up the CLI despite an existing session")
	}
	if !strings.Contains(stdout.String(), "already connected") || !strings.Contains(stdout.String(), "--force") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSetupStatusKeepsUndetectedProviderQuietAndExplainsKeySetup(t *testing.T) {
	isolateConsoleHome(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BAILIAN_TOKEN_PLAN_API_KEY", "")
	p := New(config.ProviderConfig{})
	status := p.SetupStatus()
	if status.State != provider.SetupUnavailable {
		t.Fatalf("without credentials, state = %q, want unavailable", status.State)
	}

	t.Setenv("BAILIAN_TOKEN_PLAN_API_KEY", "sk-sp-test")
	status = p.SetupStatus()
	if status.State != provider.SetupNeedsAuth || !strings.Contains(status.Detail, TokenPlanConnectCommand) {
		t.Fatalf("with a Token Plan key, status = %#v", status)
	}
}

func TestSetupStatusRecognizesInstalledBailianCLIWithoutSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture needs a Windows command shim")
	}
	isolateConsoleHome(t)
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "bl"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("BAILIAN_TOKEN_PLAN_API_KEY", "")

	status := New(config.ProviderConfig{}).SetupStatus()
	if status.State != provider.SetupNeedsAuth {
		t.Fatalf("with bl installed, state = %q, want needs_auth", status.State)
	}
	if !strings.Contains(status.Detail, "Bailian CLI found") {
		t.Fatalf("detail = %q, want installed CLI explanation", status.Detail)
	}
}

func TestEnrolledTokenPlanSourceUsesOnlyItsConsoleFile(t *testing.T) {
	home := isolateConsoleHome(t)
	writeConsoleSession(t, home)
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("Token Plan did not expose source capability")
	}
	sourced, err := capability.NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "console-file", Ref: filepath.Join(home, "missing.json")}})
	if err != nil {
		t.Fatal(err)
	}
	if sourced.IsConfigured() {
		t.Fatal("explicit Token Plan source borrowed the ambient console session")
	}
	data, err := sourced.FetchUsage(context.Background())
	if err != nil || data.SourceID != "work" || data.SourceLabel != "Work" || !data.IsExpired {
		t.Fatalf("missing explicit source result = %#v, %v", data, err)
	}
}

func TestConsoleSessionPrefersOfficialCLIStore(t *testing.T) {
	home := isolateConsoleHome(t)
	dedicatedDir := filepath.Join(home, ".config", "clawmeter", "alibaba-token-plan")
	if err := os.MkdirAll(dedicatedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dedicatedDir, "config.json"), []byte(`{"access_token":"dedicated-session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	officialDir := filepath.Join(home, ".bailian")
	if err := os.MkdirAll(officialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(officialDir, "config.json"), []byte(`{"access_token":"official-session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(config.ProviderConfig{})
	session, ok := p.consoleSession()
	if !ok || session.accessToken != "official-session" {
		t.Fatalf("consoleSession() = %#v, %v; want official CLI session", session, ok)
	}
}

func TestConnectMissingCLIExplainsSeparateQuotaAuth(t *testing.T) {
	isolateConsoleHome(t)
	var stdout bytes.Buffer
	err := connect(context.Background(), false, &stdout, nil, func(string) (string, error) {
		return "", errors.New("not found")
	}, nil)
	if err == nil {
		t.Fatal("Connect unexpectedly succeeded without a CLI")
	}
	message := err.Error()
	for _, want := range []string{OfficialCLIInstallCommand, TokenPlanConnectCommand, OfficialCLIURL, "model requests", "account-level Token Plan quota"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "access_token") || strings.Contains(message, "test-session") {
		t.Fatalf("error contains session material: %q", message)
	}
}

func TestConnectRunsOfficialLoginAndVerifiesSession(t *testing.T) {
	home := isolateConsoleHome(t)
	var stdout bytes.Buffer
	var gotExecutable string
	run := func(_ context.Context, executable string, _, _ io.Writer) error {
		gotExecutable = executable
		writeConsoleSession(t, home)
		return nil
	}
	err := connect(context.Background(), false, &stdout, nil, func(name string) (string, error) {
		if name == "bl" {
			return "/usr/local/bin/bl", nil
		}
		return "", errors.New("not found")
	}, run)
	if err != nil {
		t.Fatal(err)
	}
	if gotExecutable != "/usr/local/bin/bl" {
		t.Fatalf("executable = %q", gotExecutable)
	}
	if !strings.Contains(stdout.String(), "quota access connected") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestConnectDoesNotClaimSuccessWithoutSession(t *testing.T) {
	isolateConsoleHome(t)
	run := func(_ context.Context, _ string, _, _ io.Writer) error { return nil }
	err := connect(context.Background(), false, nil, nil, func(string) (string, error) {
		return "/usr/local/bin/bl", nil
	}, run)
	if err == nil || !strings.Contains(err.Error(), "no console session was found") {
		t.Fatalf("error = %v", err)
	}
}
