// Package provider defines the interface and types for AI service providers.
package provider

import (
	"context"
	"fmt"
	"sort"
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
	Limit       int       `json:"limit,omitempty"`        // Optional: actual limit number (e.g., 50 requests)
	Used        int       `json:"used,omitempty"`         // Optional: actual usage number
}

// UsageData contains usage information for a provider.
type UsageData struct {
	Provider  string        `json:"provider"`             // Provider name
	FetchedAt time.Time     `json:"fetched_at"`           // When this data was fetched
	Windows   []UsageWindow `json:"windows"`              // Usage windows (providers may have 1 or more)
	IsExpired bool          `json:"is_expired,omitempty"` // True if credentials are expired
	Error     string        `json:"error,omitempty"`      // Error message if fetch failed
}

// IsHealthy returns true if the usage data was fetched successfully.
func (u *UsageData) IsHealthy() bool {
	return u.Error == "" && !u.IsExpired
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
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
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

// GetConfigured returns only providers that are properly configured, in deterministic order.
func (r *Registry) GetConfigured() []Provider {
	result := make([]Provider, 0)
	for _, p := range r.providers {
		if p.IsConfigured() {
			result = append(result, p)
		}
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
