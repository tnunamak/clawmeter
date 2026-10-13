package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

type renewalTransport func(*http.Request) (*http.Response, error)

func (f renewalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func renewalTestProvider(t *testing.T) (*Provider, *time.Time, *int, *string) {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CACHE_HOME", "LOCALAPPDATA"} {
		t.Setenv(key, root)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	oldNow, oldRunner := renewalNow, doctorRunner
	renewalNow = func() time.Time { return now }
	doctorRunner = func(context.Context, string, []string) error {
		t.Error("unexpected doctor invocation")
		return errors.New("unexpected doctor")
	}
	t.Cleanup(func() { renewalNow, doctorRunner = oldNow, oldRunner })
	calls, authorization := 0, ""
	oldTransport, oldEndpoint := http.DefaultTransport, usageEndpoint
	usageEndpoint = "https://usage.invalid/test"
	http.DefaultTransport = renewalTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.String() != usageEndpoint {
			t.Errorf("unexpected HTTP call, including renewal endpoints: %s %s", r.Method, r.URL)
			return nil, errors.New("network disabled")
		}
		authorization = r.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"five_hour":{"utilization":12,"resets_at":"2026-10-12T20:00:00Z"}}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport, usageEndpoint = oldTransport, oldEndpoint })
	dir := t.TempDir()
	writeRenewalLogin(t, dir, "expired-access", "synthetic-refresh", now.Add(-time.Hour))
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
	return p, &now, &calls, &authorization
}

func writeRenewalLogin(t *testing.T, dir, access, refresh string, expires time.Time) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"claudeAiOauth": map[string]any{"accessToken": access, "refreshToken": refresh, "expiresAt": expires.UnixMilli()},
		"unrelated":     map[string]any{"preserve": "exact bytes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func credentialBytes(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCredentialBytes(t *testing.T, dir string, want []byte) {
	t.Helper()
	if !bytes.Equal(credentialBytes(t, dir), want) {
		t.Fatal("Clawmeter modified credential file bytes")
	}
}

func assertExpiredLogin(t *testing.T, p *Provider) {
	t.Helper()
	data, err := p.FetchUsage(context.Background())
	if err != nil || data == nil {
		t.Fatalf("FetchUsage: data=%#v err=%v", data, err)
	}
	if !data.IsExpired || data.Error != expiredLoginError || data.Provider != "claude" || data.SourceID != p.SourceID() || data.SourceLabel != p.SourceLabel() || data.FetchedAt.IsZero() {
		t.Fatalf("expired shape = %#v", data)
	}
}

func TestDelegatedRenewalUsesFreshAccessWithoutOAuthRequest(t *testing.T) {
	for _, sameAccess := range []bool{false, true} {
		t.Run(map[bool]string{false: "new access", true: "new expiry"}[sameAccess], func(t *testing.T) {
			p, now, calls, authorization := renewalTestProvider(t)
			before := credentialBytes(t, p.configDir)
			var expected []byte
			access := "new-access"
			if sameAccess {
				access = "expired-access"
			}
			doctorCalls := 0
			doctorRunner = func(context.Context, string, []string) error {
				doctorCalls++
				assertCredentialBytes(t, p.configDir, before)
				expected = writeRenewalLogin(t, p.configDir, access, "new-refresh", now.Add(time.Hour))
				return nil
			}
			data, err := p.FetchUsage(context.Background())
			if err != nil || data == nil || data.IsExpired || data.Error != "" || len(data.Windows) != 1 {
				t.Fatalf("renewed fetch = %#v, %v", data, err)
			}
			if doctorCalls != 1 || *calls != 1 || *authorization != "Bearer "+access {
				t.Fatalf("doctor=%d HTTP=%d authorization=%q", doctorCalls, *calls, *authorization)
			}
			assertCredentialBytes(t, p.configDir, expected)
		})
	}
}

func TestDelegatedRenewalPersistsCooldownAcrossProviders(t *testing.T) {
	p, now, calls, _ := renewalTestProvider(t)
	before := credentialBytes(t, p.configDir)
	doctorCalls := 0
	doctorRunner = func(context.Context, string, []string) error { doctorCalls++; return nil }
	assertExpiredLogin(t, p)
	creds, err := p.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	path, err := renewalStatePath(creds)
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(state, []byte("expired-access")) || bytes.Contains(state, []byte("synthetic-refresh")) || bytes.Contains(state, []byte(p.configDir)) {
		t.Fatal("cooldown persisted credential data or source path")
	}
	restarted := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "another-id", Credential: config.CredentialRef{Kind: "config-dir", Ref: p.configDir}})
	*now = now.Add(5*time.Minute - time.Nanosecond)
	assertExpiredLogin(t, restarted)
	if doctorCalls != 1 {
		t.Fatalf("cooldown did not survive new provider: doctor=%d", doctorCalls)
	}
	*now = now.Add(time.Nanosecond)
	assertExpiredLogin(t, restarted)
	if doctorCalls != 2 || *calls != 0 {
		t.Fatalf("doctor=%d HTTP=%d", doctorCalls, *calls)
	}
	assertCredentialBytes(t, p.configDir, before)
}

func TestDelegatedRenewalChangedFingerprintBypassesCooldown(t *testing.T) {
	for _, field := range []string{"access", "refresh", "expiry"} {
		t.Run(field, func(t *testing.T) {
			p, now, calls, _ := renewalTestProvider(t)
			doctorCalls := 0
			doctorRunner = func(context.Context, string, []string) error { doctorCalls++; return nil }
			assertExpiredLogin(t, p)
			access, refresh, expiry := "expired-access", "synthetic-refresh", now.Add(-time.Hour)
			switch field {
			case "access":
				access = "user-new-access"
			case "refresh":
				refresh = "user-new-refresh"
			case "expiry":
				expiry = expiry.Add(time.Second)
			}
			expected := writeRenewalLogin(t, p.configDir, access, refresh, expiry)
			assertExpiredLogin(t, p)
			assertExpiredLogin(t, p)
			if doctorCalls != 2 || *calls != 0 {
				t.Fatalf("doctor=%d HTTP=%d", doctorCalls, *calls)
			}
			assertCredentialBytes(t, p.configDir, expected)
		})
	}
}

func TestDelegatedRenewalCooldownIsPerSource(t *testing.T) {
	p, now, _, _ := renewalTestProvider(t)
	doctorCalls := 0
	doctorRunner = func(context.Context, string, []string) error { doctorCalls++; return nil }
	assertExpiredLogin(t, p)
	dir := t.TempDir()
	before := writeRenewalLogin(t, dir, "expired-access", "synthetic-refresh", now.Add(-time.Hour))
	other := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
	assertExpiredLogin(t, other)
	if doctorCalls != 2 {
		t.Fatalf("doctor=%d, sources shared cooldown", doctorCalls)
	}
	assertCredentialBytes(t, dir, before)
}

func TestDelegatedRenewalMissingCLIAndRunnerFailure(t *testing.T) {
	for _, err := range []error{exec.ErrNotFound, context.DeadlineExceeded, errors.New("doctor failed")} {
		t.Run(err.Error(), func(t *testing.T) {
			p, _, calls, _ := renewalTestProvider(t)
			before := credentialBytes(t, p.configDir)
			doctorCalls := 0
			doctorRunner = func(context.Context, string, []string) error { doctorCalls++; return err }
			assertExpiredLogin(t, p)
			assertExpiredLogin(t, p)
			if doctorCalls != 1 || *calls != 0 {
				t.Fatalf("doctor=%d HTTP=%d", doctorCalls, *calls)
			}
			assertCredentialBytes(t, p.configDir, before)
		})
	}
}

func TestDelegatedRenewalValidLoginNeverRunsDoctor(t *testing.T) {
	p, now, calls, _ := renewalTestProvider(t)
	before := writeRenewalLogin(t, p.configDir, "valid", "refresh", now.Add(time.Hour))
	if data, err := p.FetchUsage(context.Background()); err != nil || data == nil || data.Error != "" || *calls != 1 {
		t.Fatalf("valid fetch = %#v, %v", data, err)
	}
	assertCredentialBytes(t, p.configDir, before)
}

func TestDelegatedRenewalWithoutRefreshNeverRunsDoctor(t *testing.T) {
	p, now, calls, _ := renewalTestProvider(t)
	before := writeRenewalLogin(t, p.configDir, "expired", "", now.Add(-time.Hour))
	assertExpiredLogin(t, p)
	if *calls != 0 {
		t.Fatalf("HTTP=%d", *calls)
	}
	assertCredentialBytes(t, p.configDir, before)
}

func TestDelegatedRenewalTokenOverride401NeverRunsDoctor(t *testing.T) {
	p, _, _, _ := renewalTestProvider(t)
	before := credentialBytes(t, p.configDir)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "synthetic-env-token")
	http.DefaultTransport = renewalTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer synthetic-env-token" {
			t.Error("did not read token override")
		}
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	assertExpiredLogin(t, New(config.ProviderConfig{}))
	assertCredentialBytes(t, p.configDir, before)
}

func TestDelegatedRenewalRunnerIsolationAndSourceEnvironment(t *testing.T) {
	p, _, _, _ := renewalTestProvider(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "unrelated-override")
	before := credentialBytes(t, p.configDir)
	workDir := ""
	doctorRunner = func(ctx context.Context, dir string, env []string) error {
		workDir = dir
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 || dir == p.configDir {
			t.Fatalf("doctor working dir is not fresh and empty: %q, %v", dir, err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > renewalTimeout || time.Until(deadline) < renewalTimeout-time.Second {
			t.Fatalf("doctor timeout = %v, %t", deadline, ok)
		}
		if environmentValue(env, "CLAUDE_CONFIG_DIR") != p.configDir || environmentValue(env, "CLAUDE_CODE_OAUTH_TOKEN") != "" {
			t.Fatal("doctor did not receive the source's isolated environment")
		}
		return nil
	}
	assertExpiredLogin(t, p)
	if workDir == "" {
		t.Fatal("runner was not called")
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("doctor working dir remains: %v", err)
	}
	assertCredentialBytes(t, p.configDir, before)
}

func environmentValue(env []string, key string) string {
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func TestDoctorDefaultAndPinnedEnvironments(t *testing.T) {
	p, _, _, _ := renewalTestProvider(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "ambient-dir")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "ambient-token")
	creds, err := p.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got := New(config.ProviderConfig{}).doctorEnvironment(creds); !reflect.DeepEqual(got, os.Environ()) {
		t.Fatal("default source environment changed")
	}
	pinned := &Provider{pinnedNative: true}
	env := pinned.doctorEnvironment(creds)
	if environmentValue(env, "CLAUDE_CONFIG_DIR") != "" || environmentValue(env, "CLAUDE_CODE_OAUTH_TOKEN") != "" {
		t.Fatal("pinned default retained an ambient source override")
	}
}

func TestDoctorCommandIsNonInteractiveAndCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	env := []string{"CLAUDE_CONFIG_DIR=synthetic"}
	cmd := doctorCommand(ctx, filepath.Join(dir, "fake-claude"), dir, env)
	if len(cmd.Args) != 2 || cmd.Args[1] != "doctor" || cmd.Dir != dir || !reflect.DeepEqual(cmd.Env, env) || cmd.Cancel == nil || cmd.WaitDelay != time.Second {
		t.Fatal("doctor command is not isolated and cancellable")
	}
	stdin, err := io.ReadAll(cmd.Stdin)
	if err != nil || len(stdin) != 0 || cmd.Stdout != io.Discard || cmd.Stderr != io.Discard {
		t.Fatal("doctor command is interactive or exposes output")
	}
}

func TestDoctorMissingExecutableDoesNotRunBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := runClaudeDoctor(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("missing claude unexpectedly succeeded")
	}
}

func TestDelegatedRenewalRereadsOriginalSource(t *testing.T) {
	p, now, _, authorization := renewalTestProvider(t)
	original, other := p.configDir, t.TempDir()
	otherBytes := writeRenewalLogin(t, other, "wrong-account", "refresh", now.Add(time.Hour))
	var expected []byte
	doctorRunner = func(context.Context, string, []string) error {
		expected = writeRenewalLogin(t, original, "right-account", "refresh", now.Add(time.Hour))
		p.configDir = other
		return nil
	}
	data, err := p.FetchUsage(context.Background())
	if err != nil || data == nil || data.IsExpired || *authorization != "Bearer right-account" {
		t.Fatalf("original source fetch = %#v, %v, %q", data, err, *authorization)
	}
	assertCredentialBytes(t, original, expected)
	assertCredentialBytes(t, other, otherBytes)
}

func TestDelegatedRenewalKeychainRereadDoesNotFallBack(t *testing.T) {
	p, now, _, _ := renewalTestProvider(t)
	before := credentialBytes(t, p.configDir)
	oldRead := keychainRead
	t.Cleanup(func() { keychainRead = oldRead })
	value := []byte(`{"claudeAiOauth":{"accessToken":"expired-keychain","refreshToken":"synthetic-refresh","expiresAt":1}}`)
	keychainRead = func() ([]byte, error) { return value, nil }
	old, err := p.readKeychain()
	if err != nil {
		t.Fatal(err)
	}
	doctorRunner = func(context.Context, string, []string) error {
		value = []byte(`{"claudeAiOauth":{"accessToken":"fresh-keychain","refreshToken":"synthetic-refresh","expiresAt":4102444800000}}`)
		return nil
	}
	fresh := p.renewLogin(context.Background(), old)
	if fresh == nil || fresh.AccessToken() != "fresh-keychain" || !fresh.fromKeychain {
		t.Fatal("did not reread the originating keychain")
	}
	old.ClaudeAiOauth.ExpiresAt = now.Add(-time.Hour).UnixMilli()
	doctorRunner = func(context.Context, string, []string) error {
		keychainRead = func() ([]byte, error) { return nil, errors.New("unavailable") }
		return nil
	}
	// Make the preflight reread expired too; no file fallback is allowed.
	value = []byte(`{"claudeAiOauth":{"accessToken":"expired-keychain","refreshToken":"synthetic-refresh","expiresAt":1}}`)
	if got := p.renewLogin(context.Background(), old); got != nil {
		t.Fatal("keychain reread fell back to another source")
	}
	assertCredentialBytes(t, p.configDir, before)
}

func TestDelegatedRenewalRejectsStillExpiredAndEmptyLogin(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "still expired", true: "empty access"}[empty], func(t *testing.T) {
			p, now, calls, _ := renewalTestProvider(t)
			var expected []byte
			doctorCalls := 0
			doctorRunner = func(context.Context, string, []string) error {
				doctorCalls++
				access, expiry := "changed-but-expired", now.Add(-time.Minute)
				if empty {
					access, expiry = "", now.Add(time.Hour)
				}
				expected = writeRenewalLogin(t, p.configDir, access, "refresh", expiry)
				return nil
			}
			assertExpiredLogin(t, p)
			if !empty {
				assertExpiredLogin(t, p)
			}
			if doctorCalls != 1 || *calls != 0 {
				t.Fatalf("doctor=%d HTTP=%d", doctorCalls, *calls)
			}
			assertCredentialBytes(t, p.configDir, expected)
		})
	}
}

func TestDelegatedRenewalConcurrentFailuresRunOnce(t *testing.T) {
	p, _, calls, _ := renewalTestProvider(t)
	before := credentialBytes(t, p.configDir)
	var doctorCalls atomic.Int32
	doctorRunner = func(context.Context, string, []string) error { doctorCalls.Add(1); return nil }
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assertExpiredLogin(t, p)
		}()
	}
	wg.Wait()
	if doctorCalls.Load() != 1 || *calls != 0 {
		t.Fatalf("doctor=%d HTTP=%d", doctorCalls.Load(), *calls)
	}
	assertCredentialBytes(t, p.configDir, before)
}

func TestRenewalStateLockExcludesAnotherHandleAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempt.json")
	first, err := lockRenewalState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := lockRenewalState(path); err == nil {
		second.Close()
		t.Fatal("another handle acquired the same source lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := lockRenewalState(path)
	if err != nil {
		t.Fatalf("closed lock was not released: %v", err)
	}
	second.Close()
}
