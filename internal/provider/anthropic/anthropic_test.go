package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func writeTestCredentials(t *testing.T, dir, access, refresh string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": access, "refreshToken": refresh, "expiresAt": 4102444800000}})
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSourceCapabilityListsAndValidatesClaudeKinds(t *testing.T) {
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("Claude provider did not expose source capability")
	}
	kinds := capability.SourceKinds()
	if len(kinds) != 2 || kinds[0].Kind != "native" || kinds[1].Kind != "config-dir" {
		t.Fatalf("source kinds = %#v", kinds)
	}
	for _, tc := range []struct {
		name   string
		source config.SourceConfig
		valid  bool
	}{
		{"native default", config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}, true},
		{"native named", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}, false},
		{"absolute config dir", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: "/tmp/claude-work"}}, true},
		{"relative config dir", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: "relative"}}, false},
		{"unknown kind", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "CLAUDE_CONFIG_DIR"}}, false},
	} {
		err := capability.ValidateSource(tc.source)
		if (err == nil) != tc.valid {
			t.Errorf("%s: error = %v, valid = %v", tc.name, err, tc.valid)
		}
	}
}

func TestExplicitSourcesReadAndWriteOnlyTheirProfile(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	writeTestCredentials(t, one, "one", "refresh-one")
	writeTestCredentials(t, two, "two", "refresh-two")
	cfg := config.ProviderConfig{}
	p1 := NewSource(cfg, config.SourceConfig{ID: "one", Label: "One", Credential: config.CredentialRef{Kind: "config-dir", Ref: one}})
	p2 := NewSource(cfg, config.SourceConfig{ID: "two", Label: "Two", Credential: config.CredentialRef{Kind: "config-dir", Ref: two}})
	c1, err := p1.readCredentials()
	if err != nil || c1.AccessToken() != "one" {
		t.Fatalf("profile one read = %v, %v", c1, err)
	}
	c2, err := p2.readCredentials()
	if err != nil || c2.AccessToken() != "two" {
		t.Fatalf("profile two read = %v, %v", c2, err)
	}
	c1.ClaudeAiOauth.AccessToken = "one-updated"
	if err := p1.writeCredentials(c1); err != nil {
		t.Fatal(err)
	}
	read2, err := p2.readCredentials()
	if err != nil || read2.AccessToken() != "two" {
		t.Fatalf("profile two changed after profile one write: %v, %v", read2, err)
	}
	read1, _ := p1.readCredentials()
	if read1.AccessToken() != "one-updated" {
		t.Fatalf("profile one was not updated: %s", read1.AccessToken())
	}
}

func TestSymlinkedCredentialsPreserveWriteProvenance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires Windows developer mode")
	}
	targetDir, linkDir := t.TempDir(), t.TempDir()
	writeTestCredentials(t, targetDir, "before", "refresh")
	target := filepath.Join(targetDir, ".credentials.json")
	link := filepath.Join(linkDir, ".credentials.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: linkDir}})
	creds, err := p.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	creds.ClaudeAiOauth.AccessToken = "after"
	if err := p.writeCredentials(creds); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("credential symlink was replaced: info=%v err=%v", info, err)
	}
	updated, err := p.readCredentials()
	if err != nil || updated.AccessToken() != "after" {
		t.Fatalf("symlink target was not updated: creds=%v err=%v", updated, err)
	}
}

func TestLegacyConfigDirIsHonored(t *testing.T) {
	dir := t.TempDir()
	writeTestCredentials(t, dir, "legacy", "")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	p := New(config.ProviderConfig{})
	creds, err := p.readCredentials()
	if err != nil || creds.AccessToken() != "legacy" {
		t.Fatalf("legacy config dir read = %v, %v", creds, err)
	}
}

func TestNativeDefaultSourcePreservesLegacyCredentialResolution(t *testing.T) {
	dir := t.TempDir()
	writeTestCredentials(t, dir, "native-default", "")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	p := NewNativeSource(config.ProviderConfig{}, config.SourceConfig{ID: "default", Label: "Default"})
	creds, err := p.readCredentials()
	if err != nil || creds.AccessToken() != "native-default" {
		t.Fatalf("native default read = %v, %v", creds, err)
	}
	if p.SourceID() != "default" || p.SourceLabel() != "Default" || p.SourceRevision() != "" {
		t.Fatalf("native default identity = %q/%q revision=%q", p.SourceID(), p.SourceLabel(), p.SourceRevision())
	}
}

func TestCredentialsWithoutFileProvenanceCannotPersist(t *testing.T) {
	p := New(config.ProviderConfig{})
	if err := p.writeCredentials(&Credentials{}); err == nil || strings.Contains(err.Error(), string(filepath.Separator)) {
		t.Fatalf("write error = %v, want safe non-persist error", err)
	}
}

func TestSourceRevisionIsStableSecretFreeAndChangesWithFile(t *testing.T) {
	dir := t.TempDir()
	writeTestCredentials(t, dir, "one", "refresh")
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
	first := p.SourceRevision()
	if first == "" || strings.Contains(first, dir) {
		t.Fatalf("revision = %q, want opaque non-path value", first)
	}
	data := []byte(`{"claudeAiOauth":{"accessToken":"two","refreshToken":"refresh","expiresAt":4102444800000}}`)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	second := p.SourceRevision()
	if second == first {
		t.Fatal("credential file change did not change source revision")
	}
	if err := os.Remove(filepath.Join(dir, ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	missing := p.SourceRevision()
	if missing == "" || missing == second || strings.Contains(missing, dir) {
		t.Fatalf("missing-file revision = %q, want distinct opaque provenance", missing)
	}
}

func TestUsageResponseDoesNotTurnMissingUtilizationIntoZero(t *testing.T) {
	var response usageResponse
	if err := json.Unmarshal([]byte(`{"five_hour":{"resets_at":"2026-08-01T00:00:00Z"}}`), &response); err != nil {
		t.Fatal(err)
	}
	data := &provider.UsageData{Provider: "claude"}
	addUsageWindows(data, response)
	if len(data.Windows) != 0 {
		t.Fatalf("windows = %#v, want missing utilization omitted", data.Windows)
	}
}

func TestUsageResponsePreservesExplicitZero(t *testing.T) {
	var response usageResponse
	if err := json.Unmarshal([]byte(`{"five_hour":{"utilization":0,"resets_at":"2026-08-01T00:00:00Z"}}`), &response); err != nil {
		t.Fatal(err)
	}
	data := &provider.UsageData{Provider: "claude"}
	addUsageWindows(data, response)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 {
		t.Fatalf("windows = %#v, want explicit zero usage", data.Windows)
	}
}

func TestExtraUsageRequiresExplicitUtilizationOrAmounts(t *testing.T) {
	var response usageResponse
	if err := json.Unmarshal([]byte(`{"extra_usage":{"is_enabled":true,"monthly_limit":100}}`), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response.ExtraUsage.utilization(); ok {
		t.Fatal("missing used amount was converted into zero utilization")
	}
	if err := json.Unmarshal([]byte(`{"extra_usage":{"is_enabled":true,"monthly_limit":100,"used_credits":0}}`), &response); err != nil {
		t.Fatal(err)
	}
	if utilization, ok := response.ExtraUsage.utilization(); !ok || utilization != 0 {
		t.Fatalf("explicit zero utilization = %v, %t; want 0, true", utilization, ok)
	}
}

func TestAddUsageWindowsSkipsWindowsWithoutResetTime(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		FiveHour:       &usageWindow{Utilization: float64Ptr(12), ResetsAt: reset},
		SevenDaySonnet: &usageWindow{Utilization: float64Ptr(0)},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("len(data.Windows) = %d, want 1", len(data.Windows))
	}
	if got := data.Windows[0].Name; got != "5h" {
		t.Fatalf("window name = %q, want 5h", got)
	}
	if data.Windows[0].ResetsAt.IsZero() {
		t.Fatal("5h reset time should be preserved")
	}
}

func TestAddUsageWindowsIncludesNormalizedScopedModelLimits(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 7, 1, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		Limits: []usageLimit{
			{
				Kind:     "weekly_scoped",
				Percent:  float64Ptr(42),
				ResetsAt: reset,
				Scope: &usageLimitScope{
					Model: &usageLimitModelScope{DisplayName: "Fable"},
				},
			},
		},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("len(data.Windows) = %d, want 1", len(data.Windows))
	}
	got := data.Windows[0]
	if got.Name != "7d Fable" || got.DisplayName != "7 days (Fable)" {
		t.Fatalf("window = %q/%q, want 7d Fable/7 days (Fable)", got.Name, got.DisplayName)
	}
	if got.Utilization != 42 || !got.ResetsAt.Equal(reset) {
		t.Fatalf("window usage/reset = %.0f/%s, want 42/%s", got.Utilization, got.ResetsAt, reset)
	}
}

func TestAddUsageWindowsDeduplicatesLegacyAndNormalizedLimits(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 7, 1, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		FiveHour: &usageWindow{Utilization: float64Ptr(12), ResetsAt: reset},
		SevenDay: &usageWindow{Utilization: float64Ptr(34), ResetsAt: reset.Add(24 * time.Hour)},
		Limits: []usageLimit{
			{Kind: "session", Percent: float64Ptr(56), ResetsAt: reset},
			{Kind: "weekly_all", Percent: float64Ptr(78), ResetsAt: reset.Add(24 * time.Hour)},
		},
	})

	if len(data.Windows) != 2 {
		t.Fatalf("len(data.Windows) = %d, want legacy windows only", len(data.Windows))
	}
	if data.Windows[0].Name != "5h" || data.Windows[0].Utilization != 12 {
		t.Fatalf("first window = %+v, want legacy 5h", data.Windows[0])
	}
	if data.Windows[1].Name != "7d All" || data.Windows[1].Utilization != 34 {
		t.Fatalf("second window = %+v, want legacy 7d All", data.Windows[1])
	}
}

func TestUsageUnavailableWhenMainWindowsAreZeroAndModelResetMissing(t *testing.T) {
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	resp := usageResponse{
		FiveHour:       &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset},
		SevenDay:       &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset.Add(7 * time.Hour)},
		SevenDaySonnet: &usageWindow{Utilization: float64Ptr(0)},
	}

	if !resp.usageUnavailable() {
		t.Fatal("usageUnavailable() = false, want true")
	}
}

func TestUsageUnavailableAllowsRealZeroWhenModelWindowsAreAbsent(t *testing.T) {
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	resp := usageResponse{
		FiveHour: &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset},
		SevenDay: &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset.Add(7 * time.Hour)},
	}

	if resp.usageUnavailable() {
		t.Fatal("usageUnavailable() = true, want false")
	}
}

func float64Ptr(value float64) *float64 { return &value }
