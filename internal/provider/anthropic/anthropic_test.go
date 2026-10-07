package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
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

func writeTestProfile(t *testing.T, path, org string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"organizationUuid": org, "emailAddress": "user@example.com"}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeOrganizationUUIDReadsEachProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	native := New(config.ProviderConfig{})
	if got := native.ClaudeOrganizationUUID(); got != "" {
		t.Fatalf("no profile: org = %q, want empty", got)
	}
	writeTestProfile(t, filepath.Join(home, ".claude", ".claude.json"), "org-fallback")
	if got := native.ClaudeOrganizationUUID(); got != "org-fallback" {
		t.Fatalf("fallback profile: org = %q", got)
	}
	writeTestProfile(t, filepath.Join(home, ".claude.json"), "org-home")
	if got := native.ClaudeOrganizationUUID(); got != "org-home" {
		t.Fatalf("home profile: org = %q, want ~/.claude.json first", got)
	}
	dir := t.TempDir()
	writeTestProfile(t, filepath.Join(dir, ".claude.json"), "org-work")
	work := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
	if got := work.ClaudeOrganizationUUID(); got != "org-work" {
		t.Fatalf("config-dir profile: org = %q", got)
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
					Model: &usageLimitModelScope{DisplayName: "Fable 5.1"},
				},
			},
		},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("len(data.Windows) = %d, want 1", len(data.Windows))
	}
	got := data.Windows[0]
	if got.Name != "7d Fable 5.1" || got.DisplayName != "7 days (Fable 5.1)" {
		t.Fatalf("window = %q/%q, want 7d Fable 5.1/7 days (Fable 5.1)", got.Name, got.DisplayName)
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

// Regression: with several Claude sources configured, the native Default
// source must not follow an ambient CLAUDE_CONFIG_DIR / CLAUDE_CODE_OAUTH_TOKEN.
// Running the CLI from a shell set up for another account (e.g. `claude-odl`)
// otherwise made Default show that account's quota under the Default label.
func TestNativeSourceIgnoresAmbientEnvWhenMultipleSourcesConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestCredentials(t, filepath.Join(home, ".claude"), "default-account", "")
	odl := t.TempDir()
	writeTestCredentials(t, odl, "odl-account", "")

	t.Setenv("CLAUDE_CONFIG_DIR", odl)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "odl-env-token")

	sources := []config.SourceConfig{
		{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}},
		{ID: "odl", Label: "ODL", Credential: config.CredentialRef{Kind: "config-dir", Ref: odl}},
	}
	cfg := config.ProviderConfig{Sources: sources}
	def, err := (sourceCapability{}).NewSource(cfg, sources[0])
	if err != nil {
		t.Fatal(err)
	}
	other, err := (sourceCapability{}).NewSource(cfg, sources[1])
	if err != nil {
		t.Fatal(err)
	}
	dc, err := def.(*Provider).readCredentials()
	if err != nil || dc.AccessToken() != "default-account" {
		t.Fatalf("default source read the wrong account: token=%q err=%v", tokenOf(dc), err)
	}
	oc, err := other.(*Provider).readCredentials()
	if err != nil || oc.AccessToken() != "odl-account" {
		t.Fatalf("odl source read the wrong account: token=%q err=%v", tokenOf(oc), err)
	}
}

// A lone native source keeps the legacy ambient resolution chain.
func TestNativeSourceAloneStillHonorsAmbientEnv(t *testing.T) {
	dir := t.TempDir()
	writeTestCredentials(t, dir, "ambient", "")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	src := config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}
	p, err := (sourceCapability{}).NewSource(config.ProviderConfig{Sources: []config.SourceConfig{src}}, src)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := p.(*Provider).readCredentials()
	if err != nil || creds.AccessToken() != "ambient" {
		t.Fatalf("lone native source = %q, %v", tokenOf(creds), err)
	}
}

func TestNativeSourceIgnoresAmbientConfigDirAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestCredentials(t, filepath.Join(home, ".claude"), "default-account", "")
	odl := t.TempDir()
	writeTestCredentials(t, odl, "odl-account", "")
	t.Setenv("CLAUDE_CONFIG_DIR", odl)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	sources := []config.SourceConfig{
		{ID: "default", Credential: config.CredentialRef{Kind: "native"}},
		{ID: "odl", Credential: config.CredentialRef{Kind: "config-dir", Ref: odl}},
	}
	p, _ := (sourceCapability{}).NewSource(config.ProviderConfig{Sources: sources}, sources[0])
	creds, err := p.(*Provider).readCredentials()
	if err != nil || creds.AccessToken() != "default-account" {
		t.Fatalf("default source followed CLAUDE_CONFIG_DIR: %q, %v", tokenOf(creds), err)
	}
}

func tokenOf(c *Credentials) string {
	if c == nil {
		return ""
	}
	return c.AccessToken()
}

// TestMain keeps every test off the developer's real macOS Keychain.
func TestMain(m *testing.M) {
	keychainRead = func() ([]byte, error) { return nil, errors.New("keychain disabled in tests") }
	os.Exit(m.Run())
}

const documentedExtraUsage = `{"extra_usage":{"is_enabled":true,"monthly_limit":50000,"used_credits":27140,"utilization":54,"currency":"USD"}}`

func extraWindowFor(t *testing.T, payload string) provider.UsageWindow {
	t.Helper()
	p := newTestProvider(t, payload, http.StatusOK, "")
	data, err := p.FetchUsage(context.Background())
	if err != nil || data.Error != "" {
		t.Fatalf("FetchUsage: %v / %q", err, data.Error)
	}
	for _, w := range data.Windows {
		if w.Name == "extra" {
			return w
		}
	}
	t.Fatalf("no extra window in %#v", data.Windows)
	return provider.UsageWindow{}
}

// newTestProvider serves body from a local server and isolates backoff state.
func newTestProvider(t *testing.T, body string, status int, retryAfter string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	requests.Store(0)
	oldEndpoint, oldCache, oldNow := usageEndpoint, userCacheDir, now
	cacheRoot := t.TempDir()
	usageEndpoint = srv.URL
	userCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { usageEndpoint, userCacheDir, now = oldEndpoint, oldCache, oldNow })
	dir := t.TempDir()
	writeTestCredentials(t, dir, "test-token", "")
	src := config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}}
	p, err := (sourceCapability{}).NewSource(config.ProviderConfig{}, src)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Provider)
}

var requests atomic.Int32

func TestExtraUsageDocumentedExampleIsCents(t *testing.T) {
	w := extraWindowFor(t, documentedExtraUsage)
	if w.Used != 27140 || w.Limit != 50000 || w.Currency != "USD" {
		t.Fatalf("window = %#v, want used 27140 limit 50000 USD (cents, unscaled)", w)
	}
	if w.Utilization != 54 {
		t.Fatalf("utilization = %v, want 54", w.Utilization)
	}
	if got := w.MoneyDetail(); got != "$271.40 / $500.00" {
		t.Fatalf("MoneyDetail = %q", got)
	}
}

func TestExtraUsageOverCapIsReportedTruthfully(t *testing.T) {
	w := extraWindowFor(t, `{"extra_usage":{"is_enabled":true,"monthly_limit":20000,"used_credits":20040,"utilization":100,"currency":"USD"}}`)
	if w.Utilization < 100.19 || w.Utilization > 100.21 {
		t.Fatalf("utilization = %v, want ~100.2 (not clamped)", w.Utilization)
	}
	if got := w.MoneyDetail(); got != "$200.40 / $200.00, over cap" {
		t.Fatalf("MoneyDetail = %q", got)
	}
}

func TestExtraUsageNonUSDCurrency(t *testing.T) {
	w := extraWindowFor(t, `{"extra_usage":{"is_enabled":true,"monthly_limit":2000,"used_credits":1658,"utilization":83,"currency":"eur"}}`)
	if got := w.MoneyDetail(); got != "16.58 EUR / 20.00 EUR" {
		t.Fatalf("MoneyDetail = %q", got)
	}
}

func TestRateLimitBacksOffAndHonorsFloor(t *testing.T) {
	p := newTestProvider(t, `{"type":"error","error":{"type":"rate_limit_error"}}`, http.StatusTooManyRequests, "0")
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return clock }

	data, _ := p.FetchUsage(context.Background())
	if !strings.Contains(data.Error, "rate limited") || requests.Load() != 1 {
		t.Fatalf("first fetch: error=%q requests=%d", data.Error, requests.Load())
	}
	clock = clock.Add(4 * time.Minute)
	data, _ = p.FetchUsage(context.Background())
	if !strings.Contains(data.Error, "rate limited") || requests.Load() != 1 {
		t.Fatalf("during backoff: error=%q requests=%d, want no new request", data.Error, requests.Load())
	}
	clock = clock.Add(2 * time.Minute) // 6 min after first 429, past the 5 min floor
	_, _ = p.FetchUsage(context.Background())
	if requests.Load() != 2 {
		t.Fatalf("after floor: requests=%d, want 2", requests.Load())
	}
	// Second consecutive 429 doubles the delay to 10 minutes.
	clock = clock.Add(8 * time.Minute)
	_, _ = p.FetchUsage(context.Background())
	if requests.Load() != 2 {
		t.Fatalf("exponential: requests=%d, want still 2 at +8m", requests.Load())
	}
	clock = clock.Add(3 * time.Minute)
	_, _ = p.FetchUsage(context.Background())
	if requests.Load() != 3 {
		t.Fatalf("exponential: requests=%d, want 3 at +11m", requests.Load())
	}
}

func TestRateLimitHonorsLongRetryAfter(t *testing.T) {
	p := newTestProvider(t, `{}`, http.StatusTooManyRequests, "1800")
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return clock }
	_, _ = p.FetchUsage(context.Background())
	clock = clock.Add(20 * time.Minute)
	_, _ = p.FetchUsage(context.Background())
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want retry-after 30m honored", requests.Load())
	}
}

func TestSuccessClearsBackoff(t *testing.T) {
	p := newTestProvider(t, `{"five_hour":{"utilization":10,"resets_at":"2026-12-01T00:00:00Z"}}`, http.StatusOK, "")
	p.recordRateLimit("0")
	if _, ok := p.backoffUntil(); !ok {
		t.Fatal("backoff not recorded")
	}
	now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	if data, err := p.FetchUsage(context.Background()); err != nil || data.Error != "" {
		t.Fatalf("fetch after backoff: %v %q", err, data.Error)
	}
	now = time.Now
	if _, ok := p.backoffUntil(); ok {
		t.Fatal("successful fetch did not clear backoff")
	}
}

func TestRawResponseIsRecordedVerbatim(t *testing.T) {
	p := newTestProvider(t, documentedExtraUsage, http.StatusOK, "")
	if _, err := p.FetchUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw := p.LastRawResponse()
	if raw == nil || raw.Status != 200 || raw.Body != documentedExtraUsage || raw.FetchedAt.IsZero() {
		t.Fatalf("raw = %#v", raw)
	}
}

// Regression: the pinned native Default source must not share cache
// provenance with the legacy ambient route, or a failed fetch could serve the
// ambient account's cached quota under the Default label.
func TestPinnedNativeSourceHasDistinctRevision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	native := config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}
	other := config.SourceConfig{ID: "odl", Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}}
	alone, _ := (sourceCapability{}).NewSource(config.ProviderConfig{Sources: []config.SourceConfig{native}}, native)
	pinned, _ := (sourceCapability{}).NewSource(config.ProviderConfig{Sources: []config.SourceConfig{native, other}}, native)
	if rev := alone.(*Provider).SourceRevision(); rev != "" {
		t.Fatalf("lone native revision = %q, want legacy empty", rev)
	}
	rev := pinned.(*Provider).SourceRevision()
	if rev == "" {
		t.Fatal("pinned native revision is empty; legacy cache entries would match")
	}
	if cache.SourceRevisionMatches(map[string]string{"claude": ""}, "claude", rev) {
		t.Fatal("legacy unrevisioned cache entry matches pinned source")
	}
	if rev != pinned.(*Provider).SourceRevision() {
		t.Fatal("pinned revision is not stable")
	}
}

func TestConcurrentRateLimitsKeepEverySourcesBackoff(t *testing.T) {
	newTestProvider(t, "", http.StatusOK, "")
	providers := make([]*Provider, 16)
	for i := range providers {
		dir := t.TempDir()
		writeTestCredentials(t, dir, "test-token", "")
		src := config.SourceConfig{ID: fmt.Sprintf("s%d", i), Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}}
		p, err := (sourceCapability{}).NewSource(config.ProviderConfig{}, src)
		if err != nil {
			t.Fatal(err)
		}
		providers[i] = p.(*Provider)
	}
	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		go func(p *Provider) {
			defer wg.Done()
			p.recordRateLimit("0")
		}(p)
	}
	wg.Wait()
	for _, p := range providers {
		if _, ok := p.backoffUntil(); !ok {
			t.Fatalf("source %s lost its backoff after concurrent 429s", p.SourceID())
		}
	}
}

func TestTokenOverrideHasNoOrganization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeTestProfile(t, filepath.Join(home, ".claude.json"), "org-home")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "other-account-token")
	if got := New(config.ProviderConfig{}).ClaudeOrganizationUUID(); got != "" {
		t.Fatalf("env token override: org = %q, want empty", got)
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	if got := New(config.ProviderConfig{OAuthToken: "configured-token"}).ClaudeOrganizationUUID(); got != "" {
		t.Fatalf("config token override: org = %q, want empty", got)
	}
}
