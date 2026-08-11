package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestSourceCapabilitySupportsOnlyNativeDefaultAndAbsoluteCodexHome(t *testing.T) {
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("OpenAI provider did not expose source capability")
	}
	kinds := capability.SourceKinds()
	if len(kinds) != 2 || kinds[0].Kind != "native" || kinds[1].Kind != "codex-home" || !kinds[1].RefIsPath {
		t.Fatalf("source kinds = %#v", kinds)
	}
	valid := config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "codex-home", Ref: filepath.Join(string(filepath.Separator), "tmp", "codex")}}
	for _, tc := range []struct {
		name   string
		source config.SourceConfig
		wantOK bool
	}{
		{"native default", config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}, true},
		{"native named", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}, false},
		{"codex home", valid, true},
		{"relative codex home", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "codex-home", Ref: "relative"}}, false},
		{"missing codex home", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "codex-home"}}, false},
		{"unknown kind", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "api-key", Ref: "sk-secret"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := capability.ValidateSource(tc.source)
			if (err == nil) != tc.wantOK {
				t.Fatalf("error = %v, wantOK = %t", err, tc.wantOK)
			}
			if err != nil && strings.Contains(err.Error(), "sk-secret") {
				t.Fatal("validation error exposed credential material")
			}
		})
	}
}

func TestExplicitHomesReadOnlyTheirAuthFileAndMissingDoesNotUseAmbientHome(t *testing.T) {
	one, two, ambient := t.TempDir(), t.TempDir(), t.TempDir()
	writeOpenAIAuth(t, one, "one", "acct-one")
	writeOpenAIAuth(t, two, "two", "acct-two")
	writeOpenAIAuth(t, ambient, "ambient", "acct-ambient")
	t.Setenv("CODEX_HOME", ambient)

	p1, err := NewSource(config.ProviderConfig{}, source("one", one))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := NewSource(config.ProviderConfig{}, source("two", two))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p    *Provider
		want string
	}{
		{p1, "one"}, {p2, "two"},
	} {
		auth, err := readAuthFile(tc.p.authDirectory())
		if err != nil || auth.Tokens == nil || auth.Tokens.AccessToken != tc.want {
			t.Fatalf("auth = %#v, err = %v; want %q", auth, err, tc.want)
		}
	}
	missing, err := NewSource(config.ProviderConfig{}, source("missing", filepath.Join(t.TempDir(), "not-there")))
	if err != nil {
		t.Fatal(err)
	}
	if missing.IsConfigured() {
		t.Fatal("missing explicit source fell back to ambient CODEX_HOME")
	}
}

func TestExplicitSubprocessEnvironmentReplacesAmbientHomeOnUnixAndWindows(t *testing.T) {
	input := []string{"PATH=/bin", "CODEX_HOME=/ambient", "codex_home=/other", "KEEP=yes"}
	if got := replaceEnv(input, "CODEX_HOME", "/chosen", false); strings.Join(got, "|") != "PATH=/bin|codex_home=/other|KEEP=yes|CODEX_HOME=/chosen" {
		t.Fatalf("unix environment = %#v", got)
	}
	if got := replaceEnv(input, "CODEX_HOME", `C:\\chosen`, true); strings.Join(got, "|") != `PATH=/bin|KEEP=yes|CODEX_HOME=C:\\chosen` {
		t.Fatalf("windows environment = %#v", got)
	}
}

func TestSourceRevisionIsOpaqueChangesWithMetadataAndDistinguishesMissing(t *testing.T) {
	dir := t.TempDir()
	writeOpenAIAuth(t, dir, "secret-access", "secret-account")
	p, err := NewSource(config.ProviderConfig{}, source("work", dir))
	if err != nil {
		t.Fatal(err)
	}
	first := p.SourceRevision()
	if first == "" || strings.Contains(first, dir) || strings.Contains(first, "secret") {
		t.Fatalf("revision = %q, not opaque", first)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"tokens":{"access_token":"a-longer-non-secret-test-value"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	second := p.SourceRevision()
	if second == first {
		t.Fatal("auth metadata change did not change revision")
	}
	if err := os.Remove(filepath.Join(dir, "auth.json")); err != nil {
		t.Fatal(err)
	}
	missing := p.SourceRevision()
	if missing == "" || missing == second || strings.Contains(missing, dir) {
		t.Fatalf("missing revision = %q", missing)
	}
}

func TestRegistrationExpandsOpenAISourcesAndKeepsIdentityOnDirectAndResetRoutes(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	writeOpenAIAuth(t, one, "one-access", "one-account")
	writeOpenAIAuth(t, two, "two-access", "two-account")
	cfg := config.ProviderConfig{Sources: []config.SourceConfig{{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, source("one", one), source("two", two)}}
	registry := provider.NewRegistry()
	if err := provider.RegisterConfigured(registry, cfg, New(cfg)); err != nil {
		t.Fatal(err)
	}
	if got := registry.GetFamily("openai"); len(got) != 3 || provider.SourceKey(got[1]) != "openai:one" || provider.SourceKey(got[2]) != "openai:two" {
		t.Fatalf("registered sources = %#v", got)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == directUsagePath {
			if r.Header.Get("Authorization") != "Bearer two-access" || r.Header.Get("ChatGPT-Account-ID") != "two-account" {
				t.Fatalf("direct provenance headers = %q/%q", r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID"))
			}
			_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":4102444800}}}`))
			return
		}
		if r.URL.Path == resetCreditsPath {
			if r.Header.Get("Authorization") != "Bearer two-access" || r.Header.Get("ChatGPT-Account-ID") != "two-account" {
				t.Fatalf("reset provenance headers = %q/%q", r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID"))
			}
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	oldUsageURL, oldUsageClient := directUsageURL, directUsageHTTPClient
	oldResetURL, oldResetClient := resetCreditsURL, resetCreditsHTTPClient
	directUsageURL, directUsageHTTPClient = server.URL+directUsagePath, server.Client()
	resetCreditsURL, resetCreditsHTTPClient = server.URL+resetCreditsPath, server.Client()
	t.Cleanup(func() {
		directUsageURL, directUsageHTTPClient = oldUsageURL, oldUsageClient
		resetCreditsURL, resetCreditsHTTPClient = oldResetURL, oldResetClient
	})
	p, _ := NewSource(config.ProviderConfig{}, source("two", two))
	data, err := p.fetchUsageDirect(context.Background(), mustReadAuth(t, two))
	if err != nil || data.SourceID != "two" || data.SourceLabel != "Two" {
		t.Fatalf("direct data = %#v, err = %v", data, err)
	}
	p.attachResetCredits(context.Background(), data)
}

func source(id, home string) config.SourceConfig {
	return config.SourceConfig{ID: id, Label: strings.Title(id), Credential: config.CredentialRef{Kind: "codex-home", Ref: home}}
}

func writeOpenAIAuth(t *testing.T, dir, access, account string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"tokens":{"access_token":"`+access+`","account_id":"`+account+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadAuth(t *testing.T, dir string) *authFile {
	t.Helper()
	auth, err := readAuthFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
