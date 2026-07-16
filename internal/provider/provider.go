// Package provider defines the interface and types for AI service providers.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Provider is the interface that all AI service providers must implement.
type Provider interface {
	// Name returns the unique identifier for this provider (e.g., "claude", "openai")
	Name() string

	// DisplayName returns the human-readable name (e.g., "Claude", "OpenAI")
	DisplayName() string

	// Description returns a short human-readable description of the provider
	Description() string

	// DashboardURL returns the URL to the provider's web dashboard
	DashboardURL() string

	// IsConfigured returns true if this provider has valid credentials/config
	IsConfigured() bool

	// FetchUsage retrieves current usage data from the provider's API
	FetchUsage(ctx context.Context) (*UsageData, error)
}

// UsageWindow represents a single usage limit window.
type UsageWindow struct {
	Name        string    `json:"name"`                   // e.g., "5h", "7d", "daily", "monthly"
	DisplayName string    `json:"display_name,omitempty"` // e.g., "5 hours", "7 days", "Daily", "Monthly"
	Utilization float64   `json:"utilization"`            // 0-100 percentage
	ResetsAt    time.Time `json:"resets_at"`              // When this window resets
	ResetPolicy string    `json:"reset_policy,omitempty"` // Provider policy when no timestamp is known
	Limit       int       `json:"limit,omitempty"`        // Optional: actual limit number (e.g., 50 requests)
	Used        int       `json:"used,omitempty"`         // Optional: actual usage number
}

// UsageBalance represents a non-resetting provider balance.
type UsageBalance struct {
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name,omitempty"`
	Total       float64 `json:"total"`
	Used        float64 `json:"used"`
	Remaining   float64 `json:"remaining"`
}

// UsageResetCredit is read-only metadata about a banked usage-limit reset.
// It is intentionally passive inventory: Clawmeter never redeems resets.
type UsageResetCredit struct {
	Status     string    `json:"status,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	ConsumedAt time.Time `json:"consumed_at,omitempty"`
}

// MarshalJSON omits unknown timestamps instead of serializing Go's zero time.
func (c UsageResetCredit) MarshalJSON() ([]byte, error) {
	type resetCreditJSON struct {
		Status     string     `json:"status,omitempty"`
		CreatedAt  *time.Time `json:"created_at,omitempty"`
		ExpiresAt  *time.Time `json:"expires_at,omitempty"`
		ConsumedAt *time.Time `json:"consumed_at,omitempty"`
	}
	out := resetCreditJSON{Status: c.Status}
	if !c.CreatedAt.IsZero() {
		createdAt := c.CreatedAt
		out.CreatedAt = &createdAt
	}
	if !c.ExpiresAt.IsZero() {
		expiresAt := c.ExpiresAt
		out.ExpiresAt = &expiresAt
	}
	if !c.ConsumedAt.IsZero() {
		consumedAt := c.ConsumedAt
		out.ConsumedAt = &consumedAt
	}
	return json.Marshal(out)
}

// UsageResetCredits summarizes banked usage-limit resets for a provider.
type UsageResetCredits struct {
	AvailableCount int                `json:"available_count"`
	Credits        []UsageResetCredit `json:"credits,omitempty"`
	FetchedAt      time.Time          `json:"fetched_at,omitempty"`
	Warning        string             `json:"warning,omitempty"`
}

// DisplayCount returns the best non-sensitive available reset count.
func (r *UsageResetCredits) DisplayCount(now time.Time) int {
	if r == nil {
		return 0
	}
	count := r.AvailableCount
	if count < 0 {
		count = 0
	}
	if count > 0 || len(r.Credits) == 0 {
		return count
	}
	return len(r.Available(now))
}

// Available returns usable, unconsumed reset credits sorted by expiry.
func (r *UsageResetCredits) Available(now time.Time) []UsageResetCredit {
	if r == nil || len(r.Credits) == 0 {
		return nil
	}
	available := make([]UsageResetCredit, 0, len(r.Credits))
	for _, credit := range r.Credits {
		if strings.ToLower(strings.TrimSpace(credit.Status)) != "available" {
			continue
		}
		if !credit.ConsumedAt.IsZero() {
			continue
		}
		if !credit.ExpiresAt.IsZero() && !credit.ExpiresAt.After(now) {
			continue
		}
		available = append(available, credit)
	}
	sort.SliceStable(available, func(i, j int) bool {
		a, b := available[i], available[j]
		if a.ExpiresAt.IsZero() {
			return false
		}
		if b.ExpiresAt.IsZero() {
			return true
		}
		return a.ExpiresAt.Before(b.ExpiresAt)
	})
	return available
}

// EarliestExpiry returns the earliest known expiry among available reset credits.
func (r *UsageResetCredits) EarliestExpiry(now time.Time) (time.Time, bool) {
	for _, credit := range r.Available(now) {
		if !credit.ExpiresAt.IsZero() {
			return credit.ExpiresAt, true
		}
	}
	return time.Time{}, false
}

// UsageData contains usage information for a provider.
type UsageData struct {
	Provider     string             `json:"provider"`                // Provider name
	FetchedAt    time.Time          `json:"fetched_at"`              // When this data was fetched
	Windows      []UsageWindow      `json:"windows"`                 // Usage windows (providers may have 1 or more)
	Balances     []UsageBalance     `json:"balances,omitempty"`      // Non-resetting balances
	ResetCredits *UsageResetCredits `json:"reset_credits,omitempty"` // Optional banked usage-limit reset metadata
	IsExpired    bool               `json:"is_expired,omitempty"`    // True if credentials are expired
	Error        string             `json:"error,omitempty"`         // Error message if fetch failed
	Stale        bool               `json:"stale,omitempty"`         // True if showing last good data after refresh failed
	Warning      string             `json:"warning,omitempty"`       // Short non-blocking data quality note
}

// Clone returns a deep-enough copy for UI/cache fallback paths.
func (u *UsageData) Clone() *UsageData {
	if u == nil {
		return nil
	}
	clone := *u
	if u.Windows != nil {
		clone.Windows = append([]UsageWindow(nil), u.Windows...)
	}
	if u.Balances != nil {
		clone.Balances = append([]UsageBalance(nil), u.Balances...)
	}
	if u.ResetCredits != nil {
		resetCredits := *u.ResetCredits
		if u.ResetCredits.Credits != nil {
			resetCredits.Credits = append([]UsageResetCredit(nil), u.ResetCredits.Credits...)
		}
		clone.ResetCredits = &resetCredits
	}
	return &clone
}

// IsHealthy returns true if the usage data was fetched successfully.
func (u *UsageData) IsHealthy() bool {
	return u.Error == "" && !u.IsExpired
}

// HasUsageWindows reports whether the data contains useful quota readings.
func (u *UsageData) HasUsageWindows() bool {
	return u != nil && len(u.UsableWindows()) > 0 && !u.IsExpired
}

// UsableWindows returns quota windows that can be compared against a reset.
func (u *UsageData) UsableWindows() []UsageWindow {
	if u == nil || len(u.Windows) == 0 {
		return nil
	}
	windows := make([]UsageWindow, 0, len(u.Windows))
	for _, window := range u.Windows {
		if window.ResetsAt.IsZero() {
			continue
		}
		windows = append(windows, window)
	}
	return windows
}

// MarkStale annotates a cloned last-good reading when the live refresh could
// not produce trustworthy data.
func (u *UsageData) MarkStale(reason string) {
	if u == nil {
		return
	}
	u.Stale = true
	u.Warning = reason
	u.Error = ""
	u.Windows = u.UsableWindows()
}

// EstablishesPrimaryUIHistory reports whether this data proves the provider
// has produced useful quota data before.
func (u *UsageData) EstablishesPrimaryUIHistory() bool {
	return u.HasUsageWindows()
}

// ShouldShowInPrimaryUI decides whether a provider belongs in the main tray
// or default status output. Auto-detected providers must prove usefulness
// before they take visual space; explicitly enabled providers remain visible
// so their setup errors are actionable.
func ShouldShowInPrimaryUI(data *UsageData, hadPriorUsefulData, explicitlyEnabled bool) bool {
	if explicitlyEnabled {
		return true
	}
	if data == nil {
		return false
	}
	if data.IsHealthy() && data.HasUsageWindows() {
		return true
	}
	if data.HasUsageWindows() {
		return true
	}
	return hadPriorUsefulData && (data.IsExpired || data.Error != "")
}

// IsTransientFetchError reports whether an error is likely to be a temporary
// transport/subprocess failure rather than an auth, quota, or setup issue.
func IsTransientFetchError(errMsg string) bool {
	lowered := strings.ToLower(errMsg)
	if lowered == "" {
		return false
	}
	transientNeedles := []string{
		"no response received",
		"without a response",
		"context deadline exceeded",
		"connection timed out",
		"client.timeout",
		"i/o timeout",
		"connection reset",
		"broken pipe",
		"unexpected eof",
	}
	for _, needle := range transientNeedles {
		if strings.Contains(lowered, needle) {
			return true
		}
	}
	if lowered == "eof" {
		return true
	}
	return false
}

// FilterUsageDataByNames returns a copy of data containing only current names.
func FilterUsageDataByNames(data map[string]*UsageData, names []string) map[string]*UsageData {
	filtered := make(map[string]*UsageData, len(names))
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	for name, usage := range data {
		if _, ok := allowed[name]; ok {
			filtered[name] = usage
		}
	}
	return filtered
}

// SetupState describes whether a provider can be polled or needs setup.
type SetupState string

const (
	SetupReady       SetupState = "ready"
	SetupNeedsAuth   SetupState = "needs_auth"
	SetupUnavailable SetupState = "unavailable"
)

// SetupStatus is a lightweight setup/discovery status for provider listings.
type SetupStatus struct {
	State  SetupState
	Detail string
}

// IsReady reports whether the provider has enough local setup to be polled.
func (s SetupStatus) IsReady() bool {
	return s.State == SetupReady
}

// SetupReporter can be implemented by providers that can distinguish
// installed-but-not-authenticated from fully unavailable.
type SetupReporter interface {
	SetupStatus() SetupStatus
}

// GetSetupStatus returns a provider's setup status.
func GetSetupStatus(p Provider) SetupStatus {
	if reporter, ok := p.(SetupReporter); ok {
		return reporter.SetupStatus()
	}
	if p.IsConfigured() {
		return SetupStatus{State: SetupReady, Detail: "credentials found"}
	}
	return SetupStatus{State: SetupUnavailable, Detail: "no credentials"}
}

// GetWindow retrieves a specific window by name.
func (u *UsageData) GetWindow(name string) (*UsageWindow, bool) {
	for i := range u.Windows {
		if u.Windows[i].Name == name {
			return &u.Windows[i], true
		}
	}
	return nil, false
}

// Registry holds all registered providers.
//
// providers is populated once at startup via Register and read-only afterward;
// filter may change at runtime (e.g. when the tray reloads config) and is
// guarded by filterMu.
type Registry struct {
	providers map[string]Provider
	filterMu  sync.RWMutex
	filter    EnabledFilter
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// SetEnabledFilter records an optional filter consulted by GetConfigured to
// exclude providers the user has explicitly disabled. Without a filter,
// GetConfigured returns all providers reporting credentials. Calling with
// nil clears any previously-set filter. Safe to call concurrently.
func (r *Registry) SetEnabledFilter(f EnabledFilter) {
	r.filterMu.Lock()
	r.filter = f
	r.filterMu.Unlock()
}

// enabledFilter returns the current filter, taking a read lock.
func (r *Registry) enabledFilter() EnabledFilter {
	r.filterMu.RLock()
	defer r.filterMu.RUnlock()
	return r.filter
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) error {
	name := p.Name()
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %q already registered", name)
	}
	r.providers[name] = p
	return nil
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// GetAll returns all registered providers in deterministic order.
func (r *Registry) GetAll() []Provider {
	result := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

// Has returns true if a provider with the given name is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.providers[name]
	return ok
}

// ConfiguredNames returns the names of providers GetConfigured would return,
// in the same deterministic order.
func (r *Registry) ConfiguredNames() []string {
	configured := r.GetConfigured()
	names := make([]string, 0, len(configured))
	for _, p := range configured {
		names = append(names, p.Name())
	}
	return names
}

// EnabledFilter decides whether a provider has been explicitly disabled by
// the user. Providers with no config entry are treated as auto-enabled when
// detected, preserving the zero-config UX.
type EnabledFilter interface {
	IsProviderDisabled(name string) bool
}

// ExplicitEnablementFilter is implemented by config filters that can
// distinguish explicit opt-in from zero-config auto-detection.
type ExplicitEnablementFilter interface {
	IsProviderExplicitlyEnabled(name string) bool
}

// AutoPollController can be implemented by providers whose credentials should
// be discoverable but not polled by default.
type AutoPollController interface {
	AutoPollByDefault() bool
}

// AutoPollByDefault reports whether credentials alone should opt a provider
// into default polling.
func AutoPollByDefault(p Provider) bool {
	if controller, ok := p.(AutoPollController); ok {
		return controller.AutoPollByDefault()
	}
	return true
}

// GetConfigured returns providers that should be polled: those with
// credentials AND not explicitly disabled by the registry's configured
// EnabledFilter. Order is deterministic.
func (r *Registry) GetConfigured() []Provider {
	filter := r.enabledFilter()
	result := make([]Provider, 0)
	for _, p := range r.providers {
		if !p.IsConfigured() {
			continue
		}
		if filter != nil && filter.IsProviderDisabled(p.Name()) {
			continue
		}
		explicitlyEnabled := false
		if explicitFilter, ok := filter.(ExplicitEnablementFilter); ok {
			explicitlyEnabled = explicitFilter.IsProviderExplicitlyEnabled(p.Name())
		}
		if !explicitlyEnabled && !AutoPollByDefault(p) {
			continue
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

// FailureGate tracks consecutive failures per provider, suppresses transient
// errors when prior cached data is available, and backs off polling for
// persistently failing providers. Modeled after CodexBar's ConsecutiveFailureGate.
type FailureGate struct {
	streaks  map[string]int
	backoffs map[string]time.Duration
	nextPoll map[string]time.Time
}

const (
	baseBackoff = 5 * time.Minute
	maxBackoff  = 30 * time.Minute
)

// NewFailureGate creates a new failure gate.
func NewFailureGate() *FailureGate {
	return &FailureGate{
		streaks:  make(map[string]int),
		backoffs: make(map[string]time.Duration),
		nextPoll: make(map[string]time.Time),
	}
}

// RecordSuccess resets the failure streak and backoff for a provider.
func (g *FailureGate) RecordSuccess(name string) {
	g.streaks[name] = 0
	delete(g.backoffs, name)
	delete(g.nextPoll, name)
}

// ShouldSurfaceError increments the failure streak and returns true if the
// error should be shown to the user. On the first failure with prior data,
// the error is suppressed (returns false) to absorb transient blips.
// Also sets an exponential backoff for the next poll.
func (g *FailureGate) ShouldSurfaceError(name string, hasPriorData bool) bool {
	g.streaks[name]++

	// Set exponential backoff: 5m, 10m, 20m, 30m (cap)
	b := g.backoffs[name]
	if b == 0 {
		b = baseBackoff
	} else {
		b *= 2
	}
	if b > maxBackoff {
		b = maxBackoff
	}
	g.backoffs[name] = b
	g.nextPoll[name] = time.Now().Add(b)

	if hasPriorData && g.streaks[name] == 1 {
		return false
	}
	return true
}

// InBackoff returns true if the provider should be skipped this poll cycle.
func (g *FailureGate) InBackoff(name string) bool {
	t, ok := g.nextPoll[name]
	if !ok {
		return false
	}
	return time.Now().Before(t)
}

// MultiFetchResult contains results from fetching multiple providers.
type MultiFetchResult struct {
	Results   map[string]*UsageData
	FetchedAt time.Time
}

// FetchAllParallel fetches usage from all configured providers in parallel.
// Honors any EnabledFilter set on the registry, so explicitly disabled
// providers are skipped.
func FetchAllParallel(ctx context.Context, registry *Registry) *MultiFetchResult {
	return FetchProvidersParallel(ctx, registry.GetConfigured())
}

// FetchProvidersParallel fetches usage from the given providers in parallel.
func FetchProvidersParallel(ctx context.Context, providers []Provider) *MultiFetchResult {
	result := &MultiFetchResult{
		Results:   make(map[string]*UsageData, len(providers)),
		FetchedAt: time.Now(),
	}

	if len(providers) == 0 {
		return result
	}

	type fetchResult struct {
		name string
		data *UsageData
	}
	resultCh := make(chan fetchResult, len(providers))

	for _, p := range providers {
		go func(provider Provider) {
			data, err := provider.FetchUsage(ctx)
			if err != nil {
				data = &UsageData{
					Provider:  provider.Name(),
					FetchedAt: time.Now(),
					Error:     err.Error(),
				}
			}
			resultCh <- fetchResult{name: provider.Name(), data: data}
		}(p)
	}

	for i := 0; i < len(providers); i++ {
		res := <-resultCh
		result.Results[res.name] = res.data
	}

	return result
}
