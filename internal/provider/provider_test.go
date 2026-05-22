package provider

import (
	"context"
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
	cachedWithError := &UsageData{
		Error:   "timeout (showing cached)",
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
		{"auto cached windows shown", cachedWithError, false, false, true},
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
	}

	clone := original.Clone()
	clone.Error = "showing cached"
	clone.Windows[0].Utilization = 99

	if original.Error != "" {
		t.Fatalf("Clone mutated original error: %q", original.Error)
	}
	if original.Windows[0].Utilization != 12 {
		t.Fatalf("Clone mutated original window utilization: %.0f", original.Windows[0].Utilization)
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
