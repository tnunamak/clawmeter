// Package provider defines the interface and types for AI service providers.
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
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

// UsageSource is an optional capability implemented by providers with an
// explicitly enrolled source instance. Existing providers are the legacy
// default source and need not implement it.
type UsageSource interface {
	SourceID() string
	SourceLabel() string
}

// SourceRevision is an optional non-secret identity of the credential source.
type SourceRevisionCapability interface{ SourceRevision() string }

// CredentialSourceRevision returns a stable, one-way credential fingerprint.
// API credentials are high-entropy secrets; hashing lets separate Clawmeter
// processes validate the private (0600) cache without persisting the secret.
func CredentialSourceRevision(route, credential string) string {
	if credential == "" {
		credential = "unavailable"
	}
	revision := sha256.Sum256([]byte(route + "\x00" + credential))
	return fmt.Sprintf("%x", revision)
}

// EnrolledSourceCapability marks a source the user explicitly put in config.
// Enrolled sources remain visible when credentials become temporarily unreadable.
type EnrolledSourceCapability interface{ IsEnrolledSource() bool }

// SourceKind describes one provider-owned, non-secret source selector.
type SourceKind struct {
	Kind               string
	Summary            string
	RefUsage           string
	RefRequired        bool
	RefIsPath          bool
	RefCaseInsensitive bool
}

// SourceCapability is implemented by providers that can safely bind an
// enrolled source to an independent credential route.
type SourceCapability interface {
	SourceKinds() []SourceKind
	DefaultSource() (config.SourceConfig, bool)
	ValidateSource(config.SourceConfig) error
	NewSource(config.ProviderConfig, config.SourceConfig) (Provider, error)
}

// SourceCapabilityOf returns the provider-owned source capability, if any.
func SourceCapabilityOf(p Provider) (SourceCapability, bool) {
	capability, ok := p.(SourceCapability)
	return capability, ok
}

// ValidateSourceConfigs applies provider-owned selector validation and rejects
// two source IDs that resolve to the same credential route.
func ValidateSourceConfigs(capability SourceCapability, sources []config.SourceConfig) error {
	seenRefs := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if err := capability.ValidateSource(source); err != nil {
			return err
		}
		ref := canonicalSourceReference(capability, source)
		if ref == "" {
			continue
		}
		if _, exists := seenRefs[ref]; exists {
			return fmt.Errorf("duplicate credential reference")
		}
		seenRefs[ref] = struct{}{}
	}
	return nil
}

func canonicalSourceReference(capability SourceCapability, source config.SourceConfig) string {
	ref := strings.TrimSpace(source.Credential.Ref)
	if ref == "" {
		return ""
	}
	for _, kind := range capability.SourceKinds() {
		if kind.Kind != source.Credential.Kind {
			continue
		}
		if kind.RefIsPath {
			ref = canonicalPath(ref)
		}
		if kind.RefCaseInsensitive {
			ref = strings.ToLower(ref)
		}
		break
	}
	return ref
}

func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

// RegisterConfigured registers a legacy provider or its explicitly enrolled
// sources. Source construction is provider-owned; core code only expands
// identity and applies the common registration rules.
func RegisterConfigured(registry *Registry, cfg config.ProviderConfig, base Provider) error {
	if len(cfg.Sources) == 0 {
		return registry.Register(base)
	}

	capability, ok := SourceCapabilityOf(base)
	if !ok {
		return fmt.Errorf("provider %q does not support enrolled sources", base.Name())
	}
	if err := ValidateSourceConfigs(capability, cfg.Sources); err != nil {
		return fmt.Errorf("provider %q sources: %w", base.Name(), err)
	}

	sources := append([]config.SourceConfig(nil), cfg.Sources...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	for _, source := range sources {
		if !source.IsEnabled() {
			continue
		}
		sourced, err := capability.NewSource(cfg, source)
		if err != nil {
			return fmt.Errorf("provider %q source %q: %w", base.Name(), source.ID, err)
		}
		if err := registry.Register(sourced); err != nil {
			return err
		}
	}
	return nil
}

func SourceID(p Provider) string {
	if source, ok := p.(UsageSource); ok && strings.TrimSpace(source.SourceID()) != "" {
		return strings.TrimSpace(source.SourceID())
	}
	return "default"
}
func SourceLabel(p Provider) string {
	if source, ok := p.(UsageSource); ok && strings.TrimSpace(source.SourceLabel()) != "" {
		return strings.TrimSpace(source.SourceLabel())
	}
	return ""
}
func SourceKey(p Provider) string {
	id := SourceID(p)
	if id == "default" {
		return p.Name()
	}
	return p.Name() + ":" + id
}

func SourceRevision(p Provider) string {
	if source, ok := p.(SourceRevisionCapability); ok {
		return source.SourceRevision()
	}
	return ""
}

func IsEnrolledSource(p Provider) bool {
	if source, ok := p.(EnrolledSourceCapability); ok {
		return source.IsEnrolledSource()
	}
	return false
}

// SessionEnvironmentRequest describes one provider's explicit session-environment
// lookup. Resolvers return values only for EnvNames and never mutate the
// process environment.
type SessionEnvironmentRequest struct {
	EnvNames                        []string
	AllowSessionEnvironmentFallback bool
}

// SessionEnvironmentResolver resolves explicitly requested session environment values.
// Providers call it lazily after their native/configured credential sources.
type SessionEnvironmentResolver interface {
	ResolveSessionEnvironment(SessionEnvironmentRequest) map[string]string
}

// SessionExecutableResolver is an optional extension for resolving CLI tools
// from the user's interactive/session PATH when a GUI process inherited a
// reduced environment.
type SessionExecutableResolver interface {
	ResolveSessionExecutable(string) (string, error)
}

// SessionEnvironmentResolverConsumer accepts the process-local resolver during
// provider registration. Providers that do not use environment credentials
// do not implement it.
type SessionEnvironmentResolverConsumer interface {
	SetSessionEnvironmentResolver(SessionEnvironmentResolver)
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
	Provider     string             `json:"provider"` // Provider name
	SourceID     string             `json:"source_id,omitempty"`
	SourceLabel  string             `json:"source_label,omitempty"`
	FetchedAt    time.Time          `json:"fetched_at"`              // When this data was fetched
	Windows      []UsageWindow      `json:"windows"`                 // Usage windows (providers may have 1 or more)
	Balances     []UsageBalance     `json:"balances,omitempty"`      // Non-resetting balances
	ResetCredits *UsageResetCredits `json:"reset_credits,omitempty"` // Optional banked usage-limit reset metadata
	IsExpired    bool               `json:"is_expired,omitempty"`    // True if credentials are expired
	Error        string             `json:"error,omitempty"`         // Error message if fetch failed
	Stale        bool               `json:"stale,omitempty"`         // True if showing last good data after refresh failed
	Warning      string             `json:"warning,omitempty"`       // Short non-blocking data quality note
	// InvalidatesPriorUsage means the current response disproves the semantic
	// validity of earlier readings, so callers must not use stale fallback.
	InvalidatesPriorUsage bool `json:"-"`
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

// HasPresentableUsage reports whether the provider returned a fact worth
// showing, including windows whose reset is unknown and non-resetting
// balances. It is intentionally broader than HasUsageWindows: forecasts and
// pacing must continue to use only reset-backed windows.
func (u *UsageData) HasPresentableUsage() bool {
	return u != nil && !u.IsExpired && (len(u.Windows) > 0 || len(u.Balances) > 0)
}

// PresentationWindows returns every reported window, including windows with
// an unknown reset time. Callers must not use it for pacing or ranking.
func (u *UsageData) PresentationWindows() []UsageWindow {
	if u == nil {
		return nil
	}
	return append([]UsageWindow(nil), u.Windows...)
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
}

// EstablishesPrimaryUIHistory reports whether this data proves the provider
// has produced useful quota data before.
func (u *UsageData) EstablishesPrimaryUIHistory() bool {
	return u.HasPresentableUsage()
}

// ShouldShowInPrimaryUI decides whether a provider belongs in the main tray
// or default status output. Auto-detected providers must prove usefulness
// before they take visual space. A credential rejection is the exception: it
// is actionable evidence that an auto-detected provider needs attention, not
// a transient polling failure to hide.
func ShouldShowInPrimaryUI(data *UsageData, hadPriorUsefulData, explicitlyEnabled bool) bool {
	if explicitlyEnabled {
		return true
	}
	if data == nil {
		return false
	}
	if data.IsExpired {
		return true
	}
	if data.IsHealthy() && data.HasPresentableUsage() {
		return true
	}
	if data.HasPresentableUsage() {
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
		"error sending request for url",
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
	providers                  map[string]Provider
	sessionEnvironmentResolver SessionEnvironmentResolver
	filterMu                   sync.RWMutex
	filter                     EnabledFilter
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// SetSessionEnvironmentResolver injects the lazy resolver used by providers that
// declare environment-backed authentication. It does not resolve anything.
func (r *Registry) SetSessionEnvironmentResolver(resolver SessionEnvironmentResolver) {
	r.sessionEnvironmentResolver = resolver
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
	if r.sessionEnvironmentResolver != nil {
		if consumer, ok := p.(SessionEnvironmentResolverConsumer); ok {
			consumer.SetSessionEnvironmentResolver(r.sessionEnvironmentResolver)
		}
	}
	name := SourceKey(p)
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
	if !ok {
		for _, candidate := range r.providers {
			if candidate.Name() == name && SourceID(candidate) == "default" {
				return candidate, true
			}
		}
	}
	return p, ok
}

func (r *Registry) GetFamily(family string) []Provider {
	result := make([]Provider, 0)
	for _, p := range r.providers {
		if p.Name() == family {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return SourceKey(result[i]) < SourceKey(result[j]) })
	return result
}

// GetAll returns all registered providers in deterministic order.
func (r *Registry) GetAll() []Provider {
	result := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return SourceKey(result[i]) < SourceKey(result[j])
	})
	return result
}

// Has returns true if a provider with the given name is registered.
func (r *Registry) Has(name string) bool {
	if _, ok := r.providers[name]; ok {
		return true
	}
	for _, p := range r.providers {
		if p.Name() == name {
			return true
		}
	}
	return false
}

// ConfiguredNames returns the names of providers GetConfigured would return,
// in the same deterministic order.
func (r *Registry) ConfiguredNames() []string {
	configured := r.GetConfigured()
	names := make([]string, 0, len(configured))
	for _, p := range configured {
		names = append(names, SourceKey(p))
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

// UsageLookupCapability describes whether a provider's usage lookup is safe
// to run automatically when credentials are detected.
type UsageLookupCapability interface {
	SafeForAutoPolling() bool
}

// SafeForAutoPolling reports whether credentials alone should opt a provider
// into default polling. Providers without an explicit capability retain the
// historical default of automatic polling.
func SafeForAutoPolling(p Provider) bool {
	if capability, ok := p.(UsageLookupCapability); ok {
		return capability.SafeForAutoPolling()
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
		if !p.IsConfigured() && !IsEnrolledSource(p) {
			continue
		}
		if filter != nil && filter.IsProviderDisabled(p.Name()) {
			continue
		}
		explicitlyEnabled := false
		if explicitFilter, ok := filter.(ExplicitEnablementFilter); ok {
			explicitlyEnabled = explicitFilter.IsProviderExplicitlyEnabled(p.Name())
		}
		if !explicitlyEnabled && !SafeForAutoPolling(p) {
			continue
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return SourceKey(result[i]) < SourceKey(result[j])
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
	Results         map[string]*UsageData
	SourceRevisions map[string]string
	Errors          map[string]error
	FetchedAt       time.Time
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
		Results:         make(map[string]*UsageData, len(providers)),
		SourceRevisions: make(map[string]string, len(providers)),
		Errors:          make(map[string]error, len(providers)),
		FetchedAt:       time.Now(),
	}

	if len(providers) == 0 {
		return result
	}

	type fetchResult struct {
		name     string
		data     *UsageData
		revision string
		err      error
	}
	resultCh := make(chan fetchResult, len(providers))

	for _, p := range providers {
		go func(provider Provider) {
			var data *UsageData
			var err error
			var revisionAfter string
			for attempt := 0; attempt < 2; attempt++ {
				revisionBefore := SourceRevision(provider)
				data, err = provider.FetchUsage(ctx)
				revisionAfter = SourceRevision(provider)
				if revisionBefore == revisionAfter {
					break
				}
				if attempt == 1 {
					err = fmt.Errorf("credential source changed during refresh")
					data = &UsageData{
						Provider: provider.Name(), SourceID: SourceID(provider), SourceLabel: SourceLabel(provider),
						FetchedAt: time.Now(), Error: "credential source changed during refresh; retry",
						InvalidatesPriorUsage: true,
					}
				}
			}
			if err != nil {
				if data == nil {
					data = &UsageData{
						Provider: provider.Name(), SourceID: SourceID(provider), SourceLabel: SourceLabel(provider),
						FetchedAt: time.Now(),
						Error:     SafeFetchError(err),
					}
				}
			}
			if data != nil {
				data.Provider = provider.Name()
				data.SourceID = SourceID(provider)
				data.SourceLabel = SourceLabel(provider)
			}
			resultCh <- fetchResult{name: SourceKey(provider), data: data, revision: revisionAfter, err: err}
		}(p)
	}

	for i := 0; i < len(providers); i++ {
		res := <-resultCh
		result.Results[res.name] = res.data
		if res.err != nil {
			result.Errors[res.name] = res.err
		}
		if res.revision != "" {
			result.SourceRevisions[res.name] = res.revision
		}
	}

	return result
}

// SafeFetchError reduces provider and transport errors to a closed, non-secret
// message before they enter status output or the persistent cache.
func SafeFetchError(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"):
		return "rate limited"
	case strings.Contains(lower, "unauthor"), strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "expired"), strings.Contains(lower, "credential"),
		strings.Contains(lower, "token"), strings.Contains(lower, "401"), strings.Contains(lower, "403"):
		return "authentication failed"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"):
		return "connection timed out"
	case strings.Contains(lower, "connection"), strings.Contains(lower, "network"),
		strings.Contains(lower, "no such host"), strings.Contains(lower, "dns"),
		strings.Contains(lower, "no response"), strings.Contains(lower, "eof"):
		return "connection failed"
	case strings.Contains(lower, "decode"), strings.Contains(lower, "parse"),
		strings.Contains(lower, "malformed"), strings.Contains(lower, "invalid character"):
		return "provider response unavailable"
	default:
		return "provider request failed"
	}
}
