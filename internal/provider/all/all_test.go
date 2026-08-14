package all

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

type registryResolver struct{}

func (registryResolver) ResolveSessionEnvironment(provider.SessionEnvironmentRequest) map[string]string {
	return nil
}

type valueResolver map[string]string

func (r valueResolver) ResolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	values := make(map[string]string)
	for _, name := range request.EnvNames {
		if value := r[name]; value != "" {
			values[name] = value
		}
	}
	return values
}

func TestNames_IncludesKnownProviders(t *testing.T) {
	got := Names()
	if len(got) == 0 {
		t.Fatal("expected at least one provider")
	}
	// Sanity-check a handful of canonical names.
	required := []string{"alibaba", "alibaba_token", "antigravity", "claude", "deepseek", "openai", "gemini", "kimi", "kimik2", "xai"}
	have := make(map[string]bool, len(got))
	for _, n := range got {
		have[n] = true
	}
	for _, want := range required {
		if !have[want] {
			t.Errorf("expected %q in Names(), got %v", want, got)
		}
	}
}

func TestRegisterInjectsResolverIntoEveryEnvCredentialProvider(t *testing.T) {
	registry := provider.NewRegistry()
	resolver := registryResolver{}
	Register(registry, config.DefaultConfig(), resolver)

	want := map[string]bool{
		"alibaba": true, "alibaba_token": true, "antigravity": true, "claude": true, "copilot": true,
		"kimi": true, "kimik2": true, "openrouter": true, "synthetic": true,
		"xai": true, "zai": true,
	}
	for name := range want {
		p, ok := registry.Get(name)
		if !ok {
			t.Fatalf("registered provider %q is missing", name)
		}
		if _, ok := p.(provider.SessionEnvironmentResolverConsumer); !ok {
			t.Errorf("provider %q does not accept the credential resolver", name)
		}
	}
}

func TestRegisterExpandsClaudeSourcesWithoutChangingFamilyFilter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers["claude"] = config.ProviderConfig{Enabled: true, Sources: []config.SourceConfig{
		{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}},
		{ID: "work", Label: "Work", Enabled: config.Bool(true), Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}},
	}}
	r := provider.NewRegistry()
	Register(r, cfg)
	if _, ok := r.Get("claude"); !ok {
		t.Fatal("default source not registered")
	}
	if _, ok := r.Get("claude:work"); !ok {
		t.Fatal("work source not registered")
	}
	configured := r.ConfiguredNames()
	if !slices.Contains(configured, "claude") || !slices.Contains(configured, "claude:work") {
		t.Fatalf("enrolled profiles should remain visible when credentials are unavailable: %v", configured)
	}
}

func TestRegisterPreservesNativeClaudeDefaultIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers["claude"] = config.ProviderConfig{Enabled: true, Sources: []config.SourceConfig{
		{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}},
		{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}},
	}}
	r := provider.NewRegistry()
	Register(r, cfg)
	defaultSource, ok := r.Get("claude")
	if !ok || provider.SourceID(defaultSource) != "default" || provider.SourceLabel(defaultSource) != "Default" {
		t.Fatalf("native default source = %#v, registered=%v", defaultSource, ok)
	}
	if _, ok := r.Get("claude:work"); !ok {
		t.Fatal("work source not registered")
	}
}

func TestNames_Sorted(t *testing.T) {
	got := Names()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Names() not sorted: %v", got)
		}
	}
}

func TestRegistrationFactoriesMatchCanonicalProviderNames(t *testing.T) {
	for _, registration := range registrations {
		if got := registration.new(config.ProviderConfig{}).Name(); got != registration.name {
			t.Errorf("registration %q constructs provider %q", registration.name, got)
		}
	}
}

func TestEveryRegisteredProviderDeclaresAnExactSourceRoute(t *testing.T) {
	for _, registration := range registrations {
		capability, ok := provider.SourceCapabilityOf(registration.new(config.ProviderConfig{}))
		if !ok {
			t.Errorf("provider %q has no source capability", registration.name)
			continue
		}
		kinds := capability.SourceKinds()
		if len(kinds) < 2 || kinds[0].Kind != "native" {
			t.Errorf("provider %q source kinds = %#v, want native plus an exact route", registration.name, kinds)
		}
	}
}

func TestEveryProviderRegistersAndFetchesExactSourceIdentity(t *testing.T) {
	for _, registration := range registrations {
		registration := registration
		t.Run(registration.name, func(t *testing.T) {
			capability, ok := provider.SourceCapabilityOf(registration.new(config.ProviderConfig{}))
			if !ok {
				t.Fatal("missing source capability")
			}
			kind := capability.SourceKinds()[1]
			activeRef := testSourceRef(t, registration.name, kind, false)
			inactiveRef := testSourceRef(t, registration.name, kind, true)
			cfg := config.DefaultConfig()
			for _, name := range Names() {
				cfg.Providers[name] = config.ProviderConfig{Enabled: false}
			}
			cfg.Providers[registration.name] = config.ProviderConfig{Enabled: true, Sources: []config.SourceConfig{
				{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: kind.Kind, Ref: activeRef}},
				{ID: "off", Label: "Off", Enabled: config.Bool(false), Credential: config.CredentialRef{Kind: kind.Kind, Ref: inactiveRef}},
			}}

			registry := provider.NewRegistry()
			Register(registry, cfg, registryResolver{})
			sourced, ok := registry.Get(registration.name + ":work")
			if !ok {
				t.Fatal("active exact source was not registered")
			}
			if provider.SourceID(sourced) != "work" || provider.SourceLabel(sourced) != "Work" || !provider.IsEnrolledSource(sourced) {
				t.Fatalf("source identity = %q/%q enrolled=%v", provider.SourceID(sourced), provider.SourceLabel(sourced), provider.IsEnrolledSource(sourced))
			}
			if provider.SourceRevision(sourced) == "" {
				t.Fatal("exact source has no cache provenance revision")
			}
			if _, ok := registry.Get(registration.name + ":off"); ok {
				t.Fatal("disabled source was registered")
			}

			result := provider.FetchAllParallel(context.Background(), registry)
			data, ok := result.Results[registration.name+":work"]
			if !ok || data == nil {
				t.Fatalf("fetch result keys = %v", result.Results)
			}
			if data.Provider != registration.name || data.SourceID != "work" || data.SourceLabel != "Work" {
				t.Fatalf("fetch identity = provider=%q source=%q label=%q", data.Provider, data.SourceID, data.SourceLabel)
			}
			if _, ok := result.Results[registration.name+":off"]; ok {
				t.Fatal("disabled source produced a result")
			}
		})
	}
}

func TestEveryEnvironmentSourceRevisionChangesWithCredential(t *testing.T) {
	for _, registration := range registrations {
		capability, ok := provider.SourceCapabilityOf(registration.new(config.ProviderConfig{}))
		if !ok {
			continue
		}
		var kind *provider.SourceKind
		kinds := capability.SourceKinds()
		for i := range kinds {
			candidate := &kinds[i]
			if candidate.Kind != "native" && !candidate.RefIsPath {
				kind = candidate
				break
			}
		}
		if kind == nil {
			continue
		}
		registration := registration
		t.Run(registration.name, func(t *testing.T) {
			ref := "CLAWMETER_TEST_MULTI_ACCOUNT_KEY"
			cfg := config.DefaultConfig()
			for _, name := range Names() {
				cfg.Providers[name] = config.ProviderConfig{Enabled: false}
			}
			cfg.Providers[registration.name] = config.ProviderConfig{Enabled: true, Sources: []config.SourceConfig{{
				ID: "work", Credential: config.CredentialRef{Kind: kind.Kind, Ref: ref},
			}}}
			revision := func(secret string) string {
				registry := provider.NewRegistry()
				Register(registry, cfg, valueResolver{ref: secret})
				sourced, ok := registry.Get(registration.name + ":work")
				if !ok {
					t.Fatal("source was not registered")
				}
				return provider.SourceRevision(sourced)
			}
			if first, second := revision("sk-sp-account-a-secret"), revision("sk-sp-account-b-secret"); first == "" || first == second {
				t.Fatalf("credential rotation revisions = %q and %q", first, second)
			}
		})
	}
}

func testSourceRef(t *testing.T, family string, kind provider.SourceKind, inactive bool) string {
	t.Helper()
	suffix := ""
	if inactive {
		suffix = "_OFF"
	}
	if !kind.RefIsPath {
		return "CLAWMETER_TEST_" + suffix + "MULTI_ACCOUNT_KEY"
	}
	base := filepath.Join(t.TempDir(), family+suffix)
	switch kind.Kind {
	case "credential-file":
		if family == "copilot" {
			return filepath.Join(base, "hosts.json")
		}
		return filepath.Join(base, "credentials.json")
	case "token-file":
		return filepath.Join(base, "oauth_creds.json")
	case "quota-file":
		return filepath.Join(base, "quota.xml")
	case "console-file":
		return filepath.Join(base, "console.json")
	default:
		return base
	}
}

func TestInvalidConfiguredSourcesDoNotRegisterLegacyBase(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers["openai"] = config.ProviderConfig{Sources: []config.SourceConfig{{
		ID: "work", Credential: config.CredentialRef{Kind: "unknown", Ref: "slot"},
	}}}
	registry := provider.NewRegistry()
	oldStderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	Register(registry, cfg)
	_ = write.Close()
	os.Stderr = oldStderr
	var output bytes.Buffer
	_, _ = output.ReadFrom(read)
	_ = read.Close()
	if _, ok := registry.Get("openai"); ok {
		t.Fatal("unsupported configured source registered the legacy base provider")
	}
	if !bytes.Contains(output.Bytes(), []byte(`provider "openai" source "work" has unsupported credential kind "unknown"`)) {
		t.Fatalf("registration error was not visible: %q", output.String())
	}
}

func TestIsKnown(t *testing.T) {
	if !IsKnown("openai") {
		t.Error("openai should be known")
	}
	if !IsKnown("codex") {
		t.Error("codex alias should be known")
	}
	if !IsKnown("grok") {
		t.Error("grok alias should be known")
	}
	if !IsKnown("deep-seek") || !IsKnown("deepseek") {
		t.Error("DeepSeek names should be known")
	}
	if IsKnown("opneai") {
		t.Error("opneai (typo) must not be known")
	}
	if IsKnown("") {
		t.Error("empty string must not be known")
	}
}

func TestCanonicalName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"openai", "openai"},
		{"Codex", "openai"},
		{"grok", "xai"},
		{"x.ai", "xai"},
		{"token-plan", "alibaba_token"},
		{"Alibaba-Token-Plan", "alibaba_token"},
		{"deep-seek", "deepseek"},
	}
	for _, tt := range tests {
		got, ok := CanonicalName(tt.in)
		if !ok {
			t.Fatalf("CanonicalName(%q) not ok", tt.in)
		}
		if got != tt.want {
			t.Fatalf("CanonicalName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsCanonicalNameRejectsAliases(t *testing.T) {
	if !IsCanonicalName("openai") {
		t.Fatal("openai should be canonical")
	}
	if IsCanonicalName("codex") {
		t.Fatal("codex is an accepted alias, not a canonical config key")
	}
	if IsCanonicalName("grok") {
		t.Fatal("grok is an accepted alias, not a canonical config key")
	}
}

func TestSuggest(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"opneai", "openai"},
		{"clade", "claude"},
		{"gemni", "gemini"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Suggest(tt.in); got != tt.want {
			t.Errorf("Suggest(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSuggest_NoCloseMatch(t *testing.T) {
	// "xenobiosynthase" is far from every provider name. We expect either ""
	// or one of the known names, but in either case the test asserts the
	// function does not panic and returns a string from Names() (or empty).
	got := Suggest("xenobiosynthase")
	if got == "" {
		return
	}
	if !IsKnown(got) {
		t.Errorf("Suggest returned non-known name %q", got)
	}
}
