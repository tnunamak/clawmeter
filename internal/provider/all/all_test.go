package all

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

type registryResolver struct{}

func (registryResolver) ResolveSessionEnvironment(provider.SessionEnvironmentRequest) map[string]string {
	return nil
}

func TestNames_IncludesKnownProviders(t *testing.T) {
	got := Names()
	if len(got) == 0 {
		t.Fatal("expected at least one provider")
	}
	// Sanity-check a handful of canonical names.
	required := []string{"alibaba", "antigravity", "claude", "openai", "gemini", "kimi", "kimik2", "xai"}
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
		"alibaba": true, "antigravity": true, "claude": true, "copilot": true,
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

func TestNames_Sorted(t *testing.T) {
	got := Names()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Names() not sorted: %v", got)
		}
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
