package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

type sourcedTestProvider struct {
	id, label  string
	value      float64
	err        error
	enrolled   bool
	configured bool
}

type fakeSourceProvider struct {
	id string
}

func (p fakeSourceProvider) Name() string         { return "fake" }
func (p fakeSourceProvider) DisplayName() string  { return "Fake" }
func (p fakeSourceProvider) Description() string  { return "test" }
func (p fakeSourceProvider) DashboardURL() string { return "" }
func (p fakeSourceProvider) IsConfigured() bool   { return true }
func (p fakeSourceProvider) FetchUsage(context.Context) (*UsageData, error) {
	return &UsageData{Provider: p.Name(), SourceID: p.id}, nil
}
func (p fakeSourceProvider) SourceID() string    { return p.id }
func (p fakeSourceProvider) SourceLabel() string { return p.id }

type fakeSourceCapability struct{}

func (fakeSourceCapability) SourceKinds() []SourceKind {
	return []SourceKind{{Kind: "slot", Summary: "fake slot", RefUsage: "name"}}
}
func (fakeSourceCapability) ValidateSource(source config.SourceConfig) error {
	if source.Credential.Kind != "slot" {
		return fmt.Errorf("unsupported fake kind %q", source.Credential.Kind)
	}
	return nil
}
func (fakeSourceCapability) NewSource(_ config.ProviderConfig, source config.SourceConfig) (Provider, error) {
	return fakeSourceProvider{id: source.ID}, nil
}

type capableFakeBase struct{ fakeSourceProvider }

func (capableFakeBase) SourceKinds() []SourceKind { return fakeSourceCapability{}.SourceKinds() }
func (capableFakeBase) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "slot", Ref: "default"}}, true
}
func (capableFakeBase) ValidateSource(source config.SourceConfig) error {
	return fakeSourceCapability{}.ValidateSource(source)
}
func (capableFakeBase) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (Provider, error) {
	return fakeSourceCapability{}.NewSource(cfg, source)
}

type caseInsensitiveFakeBase struct{ capableFakeBase }

func (caseInsensitiveFakeBase) SourceKinds() []SourceKind {
	return []SourceKind{{Kind: "slot", RefCaseInsensitive: true}}
}

func TestRegisterConfiguredUsesProviderCapability(t *testing.T) {
	base := capableFakeBase{fakeSourceProvider{id: ""}}
	registry := NewRegistry()
	cfg := config.ProviderConfig{Sources: []config.SourceConfig{
		{ID: "work", Credential: config.CredentialRef{Kind: "slot", Ref: "work"}},
		{ID: "off", Enabled: config.Bool(false), Credential: config.CredentialRef{Kind: "slot", Ref: "off"}},
	}}
	if len(cfg.Sources) != 2 {
		t.Fatalf("sources = %#v", cfg.Sources)
	}
	if err := RegisterConfigured(registry, cfg, base); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("fake:work"); !ok {
		t.Fatalf("enabled source was not registered: keys=%v", func() []string {
			out := []string{}
			for _, p := range registry.GetAll() {
				out = append(out, SourceKey(p))
			}
			return out
		}())
	}
	if _, ok := registry.Get("fake:off"); ok {
		t.Fatal("disabled source was registered")
	}
}

func TestRegisterConfiguredRejectsUnsupportedProviderSources(t *testing.T) {
	cfg := config.ProviderConfig{Sources: []config.SourceConfig{{ID: "work", Credential: config.CredentialRef{Kind: "slot"}}}}
	if err := RegisterConfigured(NewRegistry(), cfg, providerWithoutCapability{}); err == nil || !strings.Contains(err.Error(), "does not support enrolled sources") {
		t.Fatalf("error = %v, want explicit unsupported capability error", err)
	}
}

func TestRegisterConfiguredRejectsCaseVariantCredentialRoutes(t *testing.T) {
	cfg := config.ProviderConfig{Sources: []config.SourceConfig{
		{ID: "personal", Credential: config.CredentialRef{Kind: "slot", Ref: "ACCOUNT_TOKEN"}},
		{ID: "work", Credential: config.CredentialRef{Kind: "slot", Ref: "account_token"}},
	}}
	err := RegisterConfigured(NewRegistry(), cfg, caseInsensitiveFakeBase{})
	if err == nil || !strings.Contains(err.Error(), "duplicate credential reference") {
		t.Fatalf("duplicate case-variant route error = %v", err)
	}
}

func (p sourcedTestProvider) Name() string         { return "claude" }
func (p sourcedTestProvider) DisplayName() string  { return "Claude" }
func (p sourcedTestProvider) Description() string  { return "test" }
func (p sourcedTestProvider) DashboardURL() string { return "" }
func (p sourcedTestProvider) IsConfigured() bool   { return !p.enrolled || p.configured }
func (p sourcedTestProvider) FetchUsage(context.Context) (*UsageData, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &UsageData{Windows: []UsageWindow{{Name: p.id, Utilization: p.value}}}, nil
}
func (p sourcedTestProvider) SourceID() string       { return p.id }
func (p sourcedTestProvider) SourceLabel() string    { return p.label }
func (p sourcedTestProvider) IsEnrolledSource() bool { return p.enrolled }

func TestSourceKeysAndFetchIsolation(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sourcedTestProvider{id: "personal", label: "Personal"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(sourcedTestProvider{id: "work", label: "Work"}); err != nil {
		t.Fatal(err)
	}
	if got := r.ConfiguredNames(); !equalSlice(got, []string{"claude:personal", "claude:work"}) {
		t.Fatalf("configured keys = %v", got)
	}
	result := FetchProvidersParallel(context.Background(), r.GetAll())
	if len(result.Results) != 2 || result.Results["claude:personal"].SourceLabel != "Personal" || result.Results["claude:work"].SourceLabel != "Work" {
		t.Fatalf("source results were not isolated: %#v", result.Results)
	}
}

type rotatingSourceProvider struct {
	revision string
	next     []string
	calls    int
}

func (p *rotatingSourceProvider) Name() string         { return "rotating" }
func (p *rotatingSourceProvider) DisplayName() string  { return "Rotating" }
func (p *rotatingSourceProvider) Description() string  { return "test" }
func (p *rotatingSourceProvider) DashboardURL() string { return "" }
func (p *rotatingSourceProvider) IsConfigured() bool   { return true }
func (p *rotatingSourceProvider) SourceRevision() string {
	return p.revision
}
func (p *rotatingSourceProvider) FetchUsage(context.Context) (*UsageData, error) {
	if p.calls < len(p.next) {
		p.revision = p.next[p.calls]
	}
	p.calls++
	return &UsageData{Provider: "rotating", Windows: []UsageWindow{{Name: "5h", Utilization: float64(70 + p.calls)}}}, nil
}

func TestFetchRetriesOnceWhenCredentialChangesInFlight(t *testing.T) {
	p := &rotatingSourceProvider{revision: "account-a", next: []string{"account-b"}}
	result := FetchProvidersParallel(context.Background(), []Provider{p})
	data := result.Results["rotating"]
	if data == nil || !data.HasPresentableUsage() || data.Windows[0].Utilization != 72 || p.calls != 2 {
		t.Fatalf("stable account B retry was not used: data=%#v calls=%d", data, p.calls)
	}
	if result.Errors["rotating"] != nil || result.SourceRevisions["rotating"] != "account-b" {
		t.Fatalf("stable retry provenance = %#v / %#v", result.Errors, result.SourceRevisions)
	}
}

func TestFetchDiscardsResponseWhenCredentialKeepsChangingInFlight(t *testing.T) {
	p := &rotatingSourceProvider{revision: "account-a", next: []string{"account-b", "account-c"}}
	result := FetchProvidersParallel(context.Background(), []Provider{p})
	data := result.Results["rotating"]
	if data == nil || data.HasPresentableUsage() || !data.InvalidatesPriorUsage {
		t.Fatalf("in-flight account A response was not discarded: %#v", data)
	}
	if data.Error != "credential source changed during refresh; retry" || result.Errors["rotating"] == nil {
		t.Fatalf("rotation error was not preserved safely: data=%#v errors=%#v", data, result.Errors)
	}
	if got := result.SourceRevisions["rotating"]; got != "account-c" {
		t.Fatalf("cached provenance = %q, want post-fetch account-c", got)
	}
}

func TestFetchResultRetainsSourceLocalErrors(t *testing.T) {
	providers := []Provider{
		sourcedTestProvider{id: "personal", value: 10},
		sourcedTestProvider{id: "work", err: errors.New("connection refused for bearer-secret user@example.com")},
	}
	result := FetchProvidersParallel(context.Background(), providers)
	if result.Errors["claude:work"] == nil || result.Errors["claude:personal"] != nil {
		t.Fatalf("source errors were not isolated: %#v", result.Errors)
	}
	if result.Results["claude:personal"] == nil || result.Results["claude:work"].Error != "connection failed" {
		t.Fatalf("source results were not preserved: %#v", result.Results)
	}
	if encoded, err := json.Marshal(result.Results); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(encoded), "bearer-secret") || strings.Contains(string(encoded), "example.com") {
		t.Fatalf("fetch result leaked raw error: %s", encoded)
	}
}

func TestSafeFetchErrorUsesClosedNonSecretMessages(t *testing.T) {
	secret := "bearer-secret user@example.com account-123"
	for _, tc := range []struct {
		raw, want string
	}{
		{"HTTP 429 " + secret, "rate limited"},
		{"HTTP 401 " + secret, "authentication failed"},
		{"context deadline exceeded " + secret, "connection timed out"},
		{"dial tcp: connection refused " + secret, "connection failed"},
		{"decode response: invalid character " + secret, "provider response unavailable"},
		{"unexpected " + secret, "provider request failed"},
	} {
		got := SafeFetchError(errors.New(tc.raw))
		if got != tc.want || strings.Contains(got, secret) || strings.Contains(got, "example.com") {
			t.Fatalf("SafeFetchError(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestGetConfiguredKeepsExplicitlyEnrolledUnavailableSource(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sourcedTestProvider{id: "work", enrolled: true, configured: false}); err != nil {
		t.Fatal(err)
	}
	if got := r.ConfiguredNames(); !equalSlice(got, []string{"claude:work"}) {
		t.Fatalf("enrolled unavailable source disappeared: %v", got)
	}
}

func TestSourceKeyCollisionRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sourcedTestProvider{id: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(sourcedTestProvider{id: "work"}); err == nil {
		t.Fatal("duplicate source key should fail")
	}
}

// stubProvider is a minimal Provider for registry testing.
type stubProvider struct {
	name       string
	configured bool
	safeLookup *bool
}

type providerWithoutCapability struct{}

func (providerWithoutCapability) Name() string         { return "legacy" }
func (providerWithoutCapability) DisplayName() string  { return "legacy" }
func (providerWithoutCapability) Description() string  { return "" }
func (providerWithoutCapability) DashboardURL() string { return "" }
func (providerWithoutCapability) IsConfigured() bool   { return true }
func (providerWithoutCapability) FetchUsage(context.Context) (*UsageData, error) {
	return &UsageData{Provider: "legacy"}, nil
}

func (s *stubProvider) Name() string         { return s.name }
func (s *stubProvider) DisplayName() string  { return s.name }
func (s *stubProvider) Description() string  { return "" }
func (s *stubProvider) DashboardURL() string { return "" }
func (s *stubProvider) IsConfigured() bool   { return s.configured }
func (s *stubProvider) SafeForAutoPolling() bool {
	if s.safeLookup == nil {
		return true
	}
	return *s.safeLookup
}
func (s *stubProvider) FetchUsage(ctx context.Context) (*UsageData, error) {
	return &UsageData{Provider: s.name, FetchedAt: time.Now()}, nil
}

// disabledSet is a tiny EnabledFilter for tests.
type disabledSet map[string]bool

func (d disabledSet) IsProviderDisabled(name string) bool { return d[name] }

type enablementSet struct {
	disabled map[string]bool
	explicit map[string]bool
}

func (e enablementSet) IsProviderDisabled(name string) bool {
	return e.disabled != nil && e.disabled[name]
}

func (e enablementSet) IsProviderExplicitlyEnabled(name string) bool {
	return e.explicit != nil && e.explicit[name]
}

func TestGetConfigured_RespectsEnabledFilter(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "alpha", configured: true})
	_ = r.Register(&stubProvider{name: "beta", configured: true})
	_ = r.Register(&stubProvider{name: "gamma", configured: false})

	// Without filter: both configured providers returned.
	names := providerNames(r.GetConfigured())
	if want := []string{"alpha", "beta"}; !equalSlice(names, want) {
		t.Errorf("no filter: got %v, want %v", names, want)
	}

	// With filter disabling beta: only alpha returned.
	r.SetEnabledFilter(disabledSet{"beta": true})
	names = providerNames(r.GetConfigured())
	if want := []string{"alpha"}; !equalSlice(names, want) {
		t.Errorf("with filter: got %v, want %v", names, want)
	}

	// Disabling an unconfigured provider has no effect.
	r.SetEnabledFilter(disabledSet{"gamma": true})
	names = providerNames(r.GetConfigured())
	if want := []string{"alpha", "beta"}; !equalSlice(names, want) {
		t.Errorf("disable-unconfigured: got %v, want %v", names, want)
	}

	// Clearing the filter restores prior behavior.
	r.SetEnabledFilter(nil)
	names = providerNames(r.GetConfigured())
	if want := []string{"alpha", "beta"}; !equalSlice(names, want) {
		t.Errorf("nil filter: got %v, want %v", names, want)
	}
}

func TestFetchAllParallel_SkipsDisabledProvider(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "alpha", configured: true})
	_ = r.Register(&stubProvider{name: "beta", configured: true})
	r.SetEnabledFilter(disabledSet{"beta": true})

	result := FetchAllParallel(context.Background(), r)
	if _, ok := result.Results["alpha"]; !ok {
		t.Error("alpha should have been fetched")
	}
	if _, ok := result.Results["beta"]; ok {
		t.Error("beta is disabled and must not be fetched")
	}
}

func TestGetConfigured_RequiresExplicitEnablementForOptInProviders(t *testing.T) {
	noAuto := false
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "default", configured: true})
	_ = r.Register(&stubProvider{name: "optin", configured: true, safeLookup: &noAuto})

	names := providerNames(r.GetConfigured())
	if want := []string{"default"}; !equalSlice(names, want) {
		t.Fatalf("without explicit opt-in: got %v, want %v", names, want)
	}

	r.SetEnabledFilter(enablementSet{explicit: map[string]bool{"optin": true}})
	names = providerNames(r.GetConfigured())
	if want := []string{"default", "optin"}; !equalSlice(names, want) {
		t.Fatalf("with explicit opt-in: got %v, want %v", names, want)
	}
}

func TestGetConfigured_DefaultsToSafeWithoutCapability(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(providerWithoutCapability{}); err != nil {
		t.Fatal(err)
	}

	if names := providerNames(r.GetConfigured()); !equalSlice(names, []string{"legacy"}) {
		t.Fatalf("without capability: got %v, want [legacy]", names)
	}
}

func providerNames(ps []Provider) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFailureGate_SuppressesFirstFailure(t *testing.T) {
	g := NewFailureGate()

	// First failure with prior data — suppressed
	if g.ShouldSurfaceError("claude", true) {
		t.Error("first failure with prior data should be suppressed")
	}

	// Second failure — surfaced
	if !g.ShouldSurfaceError("claude", true) {
		t.Error("second failure should be surfaced")
	}
}

func TestFailureGate_SurfacesFirstFailureWithoutPriorData(t *testing.T) {
	g := NewFailureGate()

	if !g.ShouldSurfaceError("claude", false) {
		t.Error("first failure without prior data should be surfaced")
	}
}

func TestFailureGate_SuccessResetsStreak(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", true) // streak=1, suppressed
	g.RecordSuccess("claude")            // reset

	// Next failure is treated as first again
	if g.ShouldSurfaceError("claude", true) {
		t.Error("failure after success reset should be suppressed")
	}
}

func TestFailureGate_PerProvider(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", true) // claude streak=1
	g.ShouldSurfaceError("gemini", true) // gemini streak=1

	// Claude's second failure surfaces
	if !g.ShouldSurfaceError("claude", true) {
		t.Error("claude second failure should surface")
	}
	// Gemini's second failure surfaces independently
	if !g.ShouldSurfaceError("gemini", true) {
		t.Error("gemini second failure should surface")
	}
}

func TestFailureGate_BackoffGrows(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", true) // backoff = 5m
	g.ShouldSurfaceError("claude", true) // backoff = 10m
	g.ShouldSurfaceError("claude", true) // backoff = 20m
	g.ShouldSurfaceError("claude", true) // backoff = 30m (cap)
	g.ShouldSurfaceError("claude", true) // backoff = 30m (stays capped)

	if g.backoffs["claude"] != maxBackoff {
		t.Errorf("backoff = %v, want %v", g.backoffs["claude"], maxBackoff)
	}
}

func TestFailureGate_InBackoff(t *testing.T) {
	g := NewFailureGate()

	if g.InBackoff("claude") {
		t.Error("should not be in backoff initially")
	}

	g.ShouldSurfaceError("claude", false) // sets nextPoll ~5m from now

	if !g.InBackoff("claude") {
		t.Error("should be in backoff after failure")
	}

	// Simulate time passing by directly setting nextPoll to the past
	g.nextPoll["claude"] = time.Now().Add(-1 * time.Second)

	if g.InBackoff("claude") {
		t.Error("should not be in backoff after time passes")
	}
}

func TestFailureGate_SuccessResetsBackoff(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", false) // sets backoff
	if !g.InBackoff("claude") {
		t.Fatal("should be in backoff")
	}

	g.RecordSuccess("claude")

	if g.InBackoff("claude") {
		t.Error("backoff should be cleared after success")
	}
}

func TestShouldShowInPrimaryUI(t *testing.T) {
	healthy := &UsageData{
		Windows: []UsageWindow{{Name: "5h", Utilization: 10, ResetsAt: time.Now().Add(time.Hour)}},
	}
	errorOnly := &UsageData{Error: "forbidden"}
	expired := &UsageData{IsExpired: true, Error: "reauth"}
	staleWithWindows := &UsageData{
		Stale:   true,
		Warning: "usage unavailable",
		Windows: []UsageWindow{{Name: "5h", Utilization: 10, ResetsAt: time.Now().Add(time.Hour)}},
	}

	tests := []struct {
		name      string
		data      *UsageData
		prior     bool
		explicit  bool
		wantShown bool
	}{
		{"auto nil hidden", nil, false, false, false},
		{"auto healthy shown", healthy, false, false, true},
		{"auto error without history hidden", errorOnly, false, false, false},
		{"auto expired without history shown", expired, false, false, true},
		{"auto expired with history shown", expired, true, false, true},
		{"auto stale windows shown", staleWithWindows, false, false, true},
		{"explicit nil shown", nil, false, true, true},
		{"explicit error shown", errorOnly, false, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldShowInPrimaryUI(tt.data, tt.prior, tt.explicit)
			if got != tt.wantShown {
				t.Fatalf("ShouldShowInPrimaryUI() = %v, want %v", got, tt.wantShown)
			}
		})
	}
}

func TestUsageDataCloneCopiesWindows(t *testing.T) {
	original := &UsageData{
		Provider: "openai",
		Windows:  []UsageWindow{{Name: "5h", Utilization: 12}},
		ResetCredits: &UsageResetCredits{
			AvailableCount: 1,
			Credits:        []UsageResetCredit{{Status: "available", ExpiresAt: time.Now().Add(24 * time.Hour)}},
		},
	}

	clone := original.Clone()
	clone.Error = "timeout"
	clone.Windows[0].Utilization = 99
	clone.ResetCredits.AvailableCount = 2
	clone.ResetCredits.Credits[0].Status = "consumed"

	if original.Error != "" {
		t.Fatalf("Clone mutated original error: %q", original.Error)
	}
	if original.Windows[0].Utilization != 12 {
		t.Fatalf("Clone mutated original window utilization: %.0f", original.Windows[0].Utilization)
	}
	if original.ResetCredits.AvailableCount != 1 {
		t.Fatalf("Clone mutated original reset count: %d", original.ResetCredits.AvailableCount)
	}
	if original.ResetCredits.Credits[0].Status != "available" {
		t.Fatalf("Clone mutated original reset credit: %q", original.ResetCredits.Credits[0].Status)
	}
}

func TestUsageResetCreditsEarliestExpiryIgnoresUnavailableCredits(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	credits := &UsageResetCredits{
		AvailableCount: 3,
		Credits: []UsageResetCredit{
			{Status: "available", ExpiresAt: now.Add(72 * time.Hour)},
			{Status: "available", ExpiresAt: now.Add(24 * time.Hour)},
			{Status: "available", ExpiresAt: now.Add(-1 * time.Hour)},
			{Status: "consumed", ExpiresAt: now.Add(2 * time.Hour)},
			{Status: "available", ExpiresAt: now.Add(3 * time.Hour), ConsumedAt: now},
		},
	}

	expiresAt, ok := credits.EarliestExpiry(now)
	if !ok {
		t.Fatal("EarliestExpiry() ok = false, want true")
	}
	if !expiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("EarliestExpiry() = %s, want %s", expiresAt, now.Add(24*time.Hour))
	}
}

func TestUsageResetCreditsDisplayCountPrefersProviderCount(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	credits := &UsageResetCredits{
		AvailableCount: 1,
		Credits: []UsageResetCredit{
			{Status: "available", ExpiresAt: now.Add(24 * time.Hour)},
			{Status: "available", ExpiresAt: now.Add(48 * time.Hour)},
		},
	}

	if got := credits.DisplayCount(now); got != 1 {
		t.Fatalf("DisplayCount() = %d, want provider available_count 1", got)
	}
}

func TestUsageResetCreditJSONOmitsUnknownTimestamps(t *testing.T) {
	expiresAt := time.Date(2026, 7, 12, 1, 41, 26, 0, time.UTC)
	data, err := json.Marshal(UsageResetCredit{Status: "available", ExpiresAt: expiresAt})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	got := string(data)
	if strings.Contains(got, "0001-01-01") {
		t.Fatalf("json output includes zero timestamp: %s", got)
	}
	if !strings.Contains(got, "expires_at") {
		t.Fatalf("json output = %s, want expires_at", got)
	}
	if strings.Contains(got, "created_at") || strings.Contains(got, "consumed_at") {
		t.Fatalf("json output = %s, want unknown timestamps omitted", got)
	}
}

func TestUsageDataHasUsageWindowsRequiresReset(t *testing.T) {
	data := &UsageData{
		Provider: "claude",
		Windows: []UsageWindow{
			{Name: "7d Sonnet", Utilization: 0},
			{Name: "7d All", Utilization: 12, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}

	if !data.HasUsageWindows() {
		t.Fatal("HasUsageWindows() = false, want true when at least one window has a reset")
	}
	got := data.UsableWindows()
	if len(got) != 1 || got[0].Name != "7d All" {
		t.Fatalf("UsableWindows() = %+v, want only reset-backed window", got)
	}
}

func TestUsageDataMarkStaleKeepsNonForecastableFacts(t *testing.T) {
	data := &UsageData{
		Provider: "claude",
		Error:    "usage unavailable",
		Windows: []UsageWindow{
			{Name: "7d Sonnet", Utilization: 0},
			{Name: "7d All", Utilization: 12, ResetsAt: time.Now().Add(24 * time.Hour)},
		},
	}

	data.MarkStale("usage unavailable")

	if !data.Stale {
		t.Fatal("MarkStale did not set Stale")
	}
	if data.Error != "" {
		t.Fatalf("Error = %q, want cleared for stale last-good data", data.Error)
	}
	if len(data.Windows) != 2 || data.Windows[0].Name != "7d Sonnet" {
		t.Fatalf("Windows = %+v, want resetless window preserved", data.Windows)
	}
}

func TestUsageDataHasPresentableUsageIncludesUnknownResetAndBalance(t *testing.T) {
	data := &UsageData{Windows: []UsageWindow{{Name: "daily", Utilization: 40}}, Balances: []UsageBalance{{Name: "credits", Remaining: 3}}}
	if !data.HasPresentableUsage() || len(data.UsableWindows()) != 0 {
		t.Fatalf("presentable=%v forecastable=%v, want presentable only", data.HasPresentableUsage(), len(data.UsableWindows()))
	}
}

func TestIsTransientFetchError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"read rateLimits response: no response received", true},
		{"codex app-server exited without a response", true},
		{"Post \"https://example\": context deadline exceeded", true},
		{"error sending request for url", true},
		{"write |1: broken pipe", true},
		{"API returned 403: forbidden", false},
		{"authentication required", false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			if got := IsTransientFetchError(tt.msg); got != tt.want {
				t.Fatalf("IsTransientFetchError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterUsageDataByNames(t *testing.T) {
	data := map[string]*UsageData{
		"claude":     {Provider: "claude"},
		"openrouter": {Provider: "openrouter"},
		"gemini":     {Provider: "gemini"},
	}

	filtered := FilterUsageDataByNames(data, []string{"claude", "gemini"})

	if _, ok := filtered["claude"]; !ok {
		t.Fatal("claude should remain")
	}
	if _, ok := filtered["gemini"]; !ok {
		t.Fatal("gemini should remain")
	}
	if _, ok := filtered["openrouter"]; ok {
		t.Fatal("openrouter should have been filtered out")
	}
}
