// Package kimi implements the Provider interface for Kimi Code CLI.
package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	usageURL     = "https://api.kimi.com/coding/v1/usages"
	tokenURL     = "https://auth.kimi.com/api/oauth/token"
	clientID     = "17e5f671-d194-4dfb-9706-5516cb48c098"
	timeout      = 5 * time.Second
	refreshThreshold = 5 * time.Minute
)

// Provider implements the provider.Provider interface for Kimi Code.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new Kimi provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "kimi"
}

// DisplayName returns the human-readable name.
func (p *Provider) DisplayName() string {
	return "Kimi"
}

// Description returns a short human-readable description.
func (p *Provider) Description() string {
	return "Moonshot Kimi (via Kimi Code CLI credentials)"
}

// DashboardURL returns the web dashboard URL.
func (p *Provider) DashboardURL() string {
	return "https://www.kimi.com/code/console"
}

// IsConfigured returns true if credentials are available.
func (p *Provider) IsConfigured() bool {
	_, err := p.readCredentials()
	return err == nil
}

// FetchUsage retrieves usage data from Kimi's API.
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	creds, err := p.readCredentials()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	// Refresh the token if expired or about to expire.
	if creds.IsExpired() || creds.ExpiresWithin(refreshThreshold) {
		if creds.RefreshToken != "" {
			refreshed, err := p.refreshAccessToken(ctx, creds.RefreshToken)
			if err == nil {
				creds = refreshed
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return &provider.UsageData{
				Provider:  p.Name(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "unauthorized — reauth in Kimi Code CLI",
			}, nil
		}
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp usageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.transformUsage(&apiResp), nil
}

// Credentials holds OAuth credentials for Kimi.
type Credentials struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    float64 `json:"expires_at"` // Unix timestamp (can be fractional)
	Scope        string  `json:"scope"`
	TokenType    string  `json:"token_type"`
}

// IsExpired checks if the token is expired.
func (c *Credentials) IsExpired() bool {
	return time.Now().Unix() >= int64(c.ExpiresAt)
}

// ExpiresWithin checks if the token expires within the given duration.
func (c *Credentials) ExpiresWithin(d time.Duration) bool {
	return time.Now().Add(d).Unix() >= int64(c.ExpiresAt)
}

// refreshAccessToken uses the refresh token to obtain a new access token,
// writes the updated credentials to disk, and returns the new credentials.
func (p *Provider) refreshAccessToken(ctx context.Context, refreshToken string) (*Credentials, error) {
	form := fmt.Sprintf("client_id=%s&grant_type=refresh_token&refresh_token=%s", clientID, refreshToken)
	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh returned %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
		Scope        string  `json:"scope"`
		TokenType    string  `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}

	creds := &Credentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    float64(time.Now().Unix()) + tokenResp.ExpiresIn,
		Scope:        tokenResp.Scope,
		TokenType:    tokenResp.TokenType,
	}

	// Write refreshed credentials back to disk so Kimi CLI and future
	// clawmeter polls pick them up.
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".kimi", "credentials", "kimi-code.json")
		data, err := json.Marshal(creds)
		if err == nil {
			os.WriteFile(path, data, 0600)
		}
	}

	return creds, nil
}

// readCredentials reads Kimi OAuth credentials from the credentials file.
func (p *Provider) readCredentials() (*Credentials, error) {
	// Check config first
	if p.cfg.OAuthToken != "" {
		return &Credentials{
			AccessToken: p.cfg.OAuthToken,
			ExpiresAt:   float64(time.Now().Add(24 * time.Hour).Unix()), // Assume valid for 24h
			TokenType:   "Bearer",
		}, nil
	}

	// Check environment variable
	if token := os.Getenv("KIMI_ACCESS_TOKEN"); token != "" {
		return &Credentials{
			AccessToken: token,
			ExpiresAt:   float64(time.Now().Add(24 * time.Hour).Unix()),
			TokenType:   "Bearer",
		}, nil
	}

	// Read from credentials file
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".kimi", "credentials", "kimi-code.json"))
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	return &creds, nil
}

// jsonInt handles JSON numbers that may be strings.
type jsonInt int

func (j *jsonInt) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as int first
	var intVal int
	if err := json.Unmarshal(data, &intVal); err == nil {
		*j = jsonInt(intVal)
		return nil
	}
	// Try to unmarshal as string
	var strVal string
	if err := json.Unmarshal(data, &strVal); err == nil {
		if intVal, err := parseInt(strVal); err == nil {
			*j = jsonInt(intVal)
			return nil
		}
	}
	return fmt.Errorf("cannot unmarshal %s as int", string(data))
}

func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// Internal API response types.

// usageResponse is the top-level response from the Kimi usage API.
type usageResponse struct {
	Usage  *usageSummary `json:"usage,omitempty"`
	Limits []limitItem   `json:"limits,omitempty"`
}

// usageSummary represents the overall usage summary (weekly limit).
type usageSummary struct {
	Name      string      `json:"name,omitempty"`
	Title     string      `json:"title,omitempty"`
	Used      jsonInt    `json:"used,omitempty"`
	Limit     jsonInt    `json:"limit,omitempty"`
	Remaining jsonInt    `json:"remaining,omitempty"`
	ResetAt   string      `json:"reset_at,omitempty"`   // ISO timestamp
	ResetTime string     `json:"resetTime,omitempty"`  // Alternative field name
	ResetIn   int         `json:"reset_in,omitempty"`   // Seconds until reset
	TTL       int         `json:"ttl,omitempty"`        // Seconds until reset
}

// limitItem represents a single rate limit window.
type limitItem struct {
	Name    string         `json:"name,omitempty"`
	Title   string         `json:"title,omitempty"`
	Scope   string         `json:"scope,omitempty"`
	Detail  limitDetail    `json:"detail,omitempty"`
	Window  *limitWindow   `json:"window,omitempty"`
	ResetAt string         `json:"reset_at,omitempty"`
	ResetIn int            `json:"reset_in,omitempty"`
}

// limitDetail contains the actual limit numbers.
type limitDetail struct {
	Name      string   `json:"name,omitempty"`
	Title     string   `json:"title,omitempty"`
	Used      jsonInt  `json:"used,omitempty"`
	Limit     jsonInt  `json:"limit,omitempty"`
	Remaining jsonInt  `json:"remaining,omitempty"`
	ResetAt   string   `json:"reset_at,omitempty"`
	ResetTime string   `json:"resetTime,omitempty"` // Alternative field name
	ResetIn   int      `json:"reset_in,omitempty"`
	TTL       int      `json:"ttl,omitempty"`
}

// limitWindow describes the time window for the limit.
type limitWindow struct {
	Duration int    `json:"duration,omitempty"`  // e.g., 300
	TimeUnit string `json:"timeUnit,omitempty"`  // e.g., "MINUTES"
}

// transformUsage converts the Kimi API response to our standard format.
func (p *Provider) transformUsage(resp *usageResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	// Process the main usage summary (weekly/daily limit)
	if resp.Usage != nil {
		window := p.usageToWindow(resp.Usage, "daily", "Daily")
		if window != nil {
			data.Windows = append(data.Windows, *window)
		}
	}

	// Process individual limits (rate limits)
	for _, limit := range resp.Limits {
		window := p.limitToWindow(&limit)
		if window != nil {
			data.Windows = append(data.Windows, *window)
		}
	}

	return data
}

// usageToWindow converts a usage summary to a UsageWindow.
func (p *Provider) usageToWindow(u *usageSummary, name, displayName string) *provider.UsageWindow {
	used := int(u.Used)
	if used == 0 && u.Remaining > 0 && u.Limit > 0 {
		used = int(u.Limit) - int(u.Remaining)
	}

	if u.Limit == 0 {
		return nil
	}

	utilization := float64(used) / float64(u.Limit) * 100
	if utilization > 100 {
		utilization = 100
	}

	// Use ResetTime if ResetAt is empty
	resetAt := u.ResetAt
	if resetAt == "" {
		resetAt = u.ResetTime
	}
	resetsAt := p.parseResetTime(resetAt, u.ResetIn, u.TTL)

	return &provider.UsageWindow{
		Name:        name,
		DisplayName: coalesce(u.Name, u.Title, displayName),
		Utilization: utilization,
		ResetsAt:    resetsAt,
		Limit:       int(u.Limit),
		Used:        used,
	}
}

// limitToWindow converts a limit item to a UsageWindow.
func (p *Provider) limitToWindow(l *limitItem) *provider.UsageWindow {
	detail := &l.Detail
	used := int(detail.Used)
	if used == 0 && detail.Remaining > 0 && detail.Limit > 0 {
		used = int(detail.Limit) - int(detail.Remaining)
	}

	if detail.Limit == 0 {
		return nil
	}

	utilization := float64(used) / float64(detail.Limit) * 100
	if utilization > 100 {
		utilization = 100
	}

	// Determine the reset time - use ResetAt first, then ResetTime
	resetAt := l.ResetAt
	if resetAt == "" {
		resetAt = detail.ResetAt
	}
	if resetAt == "" {
		resetAt = detail.ResetTime
	}

	resetIn := l.ResetIn
	if resetIn == 0 {
		resetIn = detail.ResetIn
	}
	if resetIn == 0 {
		resetIn = detail.TTL
	}

	resetsAt := p.parseResetTime(resetAt, resetIn, 0)

	// Build a meaningful name
	name := coalesce(l.Name, l.Scope, detail.Name)
	if name == "" && l.Window != nil {
		// Generate name from window duration
		name = p.windowToName(l.Window)
	}
	if name == "" {
		name = "limit"
	}

	displayName := coalesce(l.Title, detail.Title, name)

	return &provider.UsageWindow{
		Name:        name,
		DisplayName: displayName,
		Utilization: utilization,
		ResetsAt:    resetsAt,
		Limit:       int(detail.Limit),
		Used:        used,
	}
}

// windowToName generates a name from a limit window.
func (p *Provider) windowToName(w *limitWindow) string {
	duration := w.Duration
	unit := w.TimeUnit

	// Normalize unit (handle both "MINUTE" and "TIME_UNIT_MINUTE")
	unit = normalizeTimeUnit(unit)

	switch {
	case unit == "minute" && duration == 60:
		return "1h"
	case unit == "minute" && duration%60 == 0:
		return fmt.Sprintf("%dh", duration/60)
	case unit == "minute":
		return fmt.Sprintf("%dm", duration)
	case unit == "hour":
		return fmt.Sprintf("%dh", duration)
	case unit == "day":
		return fmt.Sprintf("%dd", duration)
	default:
		return fmt.Sprintf("%d%s", duration, unit)
	}
}

// normalizeTimeUnit converts various time unit formats to a standard form.
func normalizeTimeUnit(unit string) string {
	// Handle TIME_UNIT_* prefix
	if strings.HasPrefix(unit, "TIME_UNIT_") {
		unit = strings.TrimPrefix(unit, "TIME_UNIT_")
	}
	return strings.ToLower(unit)
}

// parseResetTime parses various reset time formats.
func (p *Provider) parseResetTime(resetAt string, resetIn, ttl int) time.Time {
	now := time.Now()

	// Try ISO timestamp first
	if resetAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, resetAt); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, resetAt); err == nil {
			return t
		}
	}

	// Use reset_in or ttl (seconds from now)
	seconds := resetIn
	if seconds == 0 {
		seconds = ttl
	}
	if seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}

	// Default: 7 days from now (weekly reset)
	return now.Add(7 * 24 * time.Hour)
}

// coalesce returns the first non-empty string.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Register registers the Kimi provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("kimi")
	return registry.Register(New(providerCfg))
}
