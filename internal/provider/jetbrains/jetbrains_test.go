package jetbrains

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestParseQuotaXMLRequiresExplicitLimitAndUsage(t *testing.T) {
	for _, input := range []string{
		`<application><component><option name="monthlyCreditsLimit" value="100"/></component></application>`,
		`<application><component><option name="monthlyCreditsUsed" value="0"/></component></application>`,
	} {
		if quota, err := parseQuotaXML([]byte(input)); err == nil || quota != nil {
			t.Fatalf("parseQuotaXML(%q) = %#v, %v; want incomplete data rejected", input, quota, err)
		}
	}
}

func TestParseQuotaXMLPreservesExplicitZeroAndUnknownReset(t *testing.T) {
	input := `<application><component><option name="monthlyCreditsLimit" value="100"/><option name="monthlyCreditsUsed" value="0"/></component></application>`
	quota, err := parseQuotaXML([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	data := (&Provider{}).transformQuota(quota)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("data = %#v, want explicit zero usage and unknown reset", data)
	}
}

func TestSourceCapabilityListsAndValidatesJetBrainsKinds(t *testing.T) {
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("JetBrains should expose source capability")
	}
	if len(capability.SourceKinds()) != 2 {
		t.Fatalf("source kinds = %#v, want native and quota-file", capability.SourceKinds())
	}
	for _, tc := range []struct {
		name  string
		src   config.SourceConfig
		valid bool
	}{
		{"native default", config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}, true},
		{"native named", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}, false},
		{"absolute quota file", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "quota-file", Ref: filepath.Join(t.TempDir(), "AIAssistantQuotaManager2.xml")}}, true},
		{"relative quota file", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "quota-file", Ref: "relative.xml"}}, false},
		{"unknown kind", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}}, false},
	} {
		err := capability.ValidateSource(tc.src)
		if (err == nil) != tc.valid {
			t.Fatalf("%s ValidateSource() err = %v, valid = %v", tc.name, err, tc.valid)
		}
	}
}

func TestExplicitQuotaFileSourcesAreSeparateObservationsWithoutFallback(t *testing.T) {
	// Separate JetBrains quota files are separate local observations. They are
	// not proof that JetBrains assigns separate account quota pools.
	home := t.TempDir()
	t.Setenv("HOME", home)
	native := filepath.Join(home, ".config", "JetBrains", "IDEA2026.1", "options", "AIAssistantQuotaManager2.xml")
	writeQuotaFile(t, native, 100, 90)

	one := filepath.Join(t.TempDir(), "AIAssistantQuotaManager2.xml")
	two := filepath.Join(t.TempDir(), "AIAssistantQuotaManager2.xml")
	writeQuotaFile(t, one, 100, 10)

	p1 := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "one", Label: "One", Credential: config.CredentialRef{Kind: "quota-file", Ref: one}})
	p2 := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "two", Label: "Two", Credential: config.CredentialRef{Kind: "quota-file", Ref: two}})

	if p1.SourceID() != "one" || p1.SourceLabel() != "One" || !p1.IsEnrolledSource() {
		t.Fatalf("p1 source identity = %q/%q enrolled=%v", p1.SourceID(), p1.SourceLabel(), p1.IsEnrolledSource())
	}
	if !p1.IsConfigured() {
		t.Fatal("source one should be configured from its exact quota file")
	}
	if p2.IsConfigured() {
		t.Fatal("source two missing exact quota file must not fall back to native discovery")
	}
	data := p1.transformQuota(&quotaData{MonthlyLimit: 100, MonthlyUsed: 10, HasLimit: true, HasUsed: true})
	if data.SourceID != "one" || data.SourceLabel != "One" {
		t.Fatalf("usage source identity = %q/%q", data.SourceID, data.SourceLabel)
	}
}

func TestSourceRevisionIsStableSecretFreeAndChangesWithJetBrainsQuotaFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AIAssistantQuotaManager2.xml")
	writeQuotaFile(t, path, 100, 10)
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "quota-file", Ref: path}})
	first := p.SourceRevision()
	if first == "" || strings.Contains(first, filepath.Dir(path)) {
		t.Fatalf("revision = %q, want opaque non-path value", first)
	}
	writeQuotaFile(t, path, 100, 11)
	second := p.SourceRevision()
	if second == first {
		t.Fatal("quota file change did not change source revision")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing := p.SourceRevision()
	if missing == "" || missing == second || strings.Contains(missing, filepath.Dir(path)) {
		t.Fatalf("missing-file revision = %q, want distinct opaque provenance", missing)
	}
}

func TestRegisterExpandsJetBrainsSources(t *testing.T) {
	disabled := false
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{
		"jetbrains": {
			Sources: []config.SourceConfig{
				{ID: "two", Label: "Two", Credential: config.CredentialRef{Kind: "quota-file", Ref: filepath.Join(t.TempDir(), "two.xml")}},
				{ID: "one", Label: "One", Credential: config.CredentialRef{Kind: "quota-file", Ref: filepath.Join(t.TempDir(), "one.xml")}},
				{ID: "off", Enabled: &disabled, Credential: config.CredentialRef{Kind: "quota-file", Ref: filepath.Join(t.TempDir(), "off.xml")}},
			},
		},
	}}
	registry := provider.NewRegistry()
	if err := Register(registry, cfg); err != nil {
		t.Fatal(err)
	}
	all := registry.GetAll()
	if len(all) != 2 || provider.SourceKey(all[0]) != "jetbrains:one" || provider.SourceKey(all[1]) != "jetbrains:two" {
		t.Fatalf("registered sources = %#v", all)
	}
}

func writeQuotaFile(t *testing.T, path string, limit, used int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`<application><component><option name="monthlyCreditsLimit" value="` + strconv.Itoa(limit) + `"/><option name="monthlyCreditsUsed" value="` + strconv.Itoa(used) + `"/></component></application>`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
