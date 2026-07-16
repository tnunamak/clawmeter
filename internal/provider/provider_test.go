package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// stubProvider is a minimal Provider for registry testing.
type stubProvider struct {
	name       string
	configured bool
	autoPoll   *bool
}

func (s *stubProvider) Name() string         { return s.name }
func (s *stubProvider) DisplayName() string  { return s.name }
func (s *stubProvider) Description() string  { return "" }
func (s *stubProvider) DashboardURL() string { return "" }
func (s *stubProvider) IsConfigured() bool   { return s.configured }
func (s *stubProvider) AutoPollByDefault() bool {
	if s.autoPoll == nil {
		return true
	}
	return *s.autoPoll
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
	_ = r.Register(&stubProvider{name: "optin", configured: true, autoPoll: &noAuto})

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
		{"auto expired without history hidden", expired, false, false, false},
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
