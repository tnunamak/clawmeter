// Package anthropic implements the Provider interface for Anthropic/Claude.
package anthropic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	usageURL      = "https://api.anthropic.com/api/oauth/usage"
	tokenURL      = "https://platform.claude.com/v1/oauth/token"
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	betaHeader    = "oauth-2025-04-20"
	timeout       = 15 * time.Second
)

// Provider implements the provider.Provider interface for Anthropic/Claude.
type Provider struct {
	cfg                        config.ProviderConfig
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	sourceID                   string
	sourceLabel                string
	configDir                  string
	explicitSource             bool
	enrolledSource             bool
}

func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
}

// New creates a new Anthropic provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

// NewNativeSource preserves the legacy Claude credential resolution chain while
// attaching an explicit identity to the compatibility default source.
func NewNativeSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	if strings.TrimSpace(source.Credential.Kind) == "" {
		source.Credential.Kind = "native"
	}
	p, _ := newSource(cfg, source)
	return p
}

func NewSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	p, _ := newSource(cfg, source)
	return p
}

func newSource(cfg config.ProviderConfig, source config.SourceConfig) (*Provider, error) {
	p, err := (&sourceCapability{}).NewSource(cfg, source)
	if err != nil {
		return nil, err
	}
	return p.(*Provider), nil
}

type sourceCapability struct{}

func (*Provider) SourceKinds() []provider.SourceKind { return (sourceCapability{}).SourceKinds() }
func (*Provider) DefaultSource() (config.SourceConfig, bool) {
	return (sourceCapability{}).DefaultSource()
}
func (*Provider) ValidateSource(source config.SourceConfig) error {
	return (sourceCapability{}).ValidateSource(source)
}
func (*Provider) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	return (sourceCapability{}).NewSource(cfg, source)
}

func (sourceCapability) SourceKinds() []provider.SourceKind {
	return []provider.SourceKind{
		{Kind: "native", Summary: "Provider's legacy/default credential route"},
		{Kind: "config-dir", Summary: "Claude Code credential directory; absolute path required", RefUsage: "/absolute/path", RefRequired: true, RefIsPath: true},
	}
}

func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{
		ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"},
	}, true
}

func (sourceCapability) ValidateSource(source config.SourceConfig) error {
	kind := strings.TrimSpace(source.Credential.Kind)
	switch kind {
	case "native":
		if strings.TrimSpace(source.ID) != "default" || strings.TrimSpace(source.Credential.Ref) != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "claude", source.ID)
		}
	case "config-dir":
		ref := strings.TrimSpace(source.Credential.Ref)
		if ref == "" {
			return fmt.Errorf("provider %q source %q has empty config directory", "claude", source.ID)
		}
		if !filepath.IsAbs(ref) {
			return fmt.Errorf("provider %q source %q has relative config directory", "claude", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "claude", source.ID, kind)
	}
	return nil
}

func (sourceCapability) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(source); err != nil {
		return nil, err
	}
	p := &Provider{cfg: cfg, sourceID: strings.TrimSpace(source.ID), sourceLabel: strings.TrimSpace(source.Label), enrolledSource: true}
	if source.Credential.Kind == "config-dir" {
		p.configDir = strings.TrimSpace(source.Credential.Ref)
		p.explicitSource = true
	}
	return p, nil
}

func (p *Provider) SourceID() string {
	if p.sourceID == "" {
		return "default"
	}
	return p.sourceID
}
func (p *Provider) SourceLabel() string    { return p.sourceLabel }
func (p *Provider) IsEnrolledSource() bool { return p.enrolledSource }
func (p *Provider) SourceRevision() string {
	if !p.explicitSource {
		return ""
	}
	path := filepath.Join(p.configDir, ".credentials.json")
	canonical, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical+"\x00unavailable")))
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%o", canonical, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))))
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "claude"
}

// DisplayName returns the human-readable name.
func (p *Provider) DisplayName() string {
	return "Claude"
}

// Description returns a short human-readable description.
func (p *Provider) Description() string {
	return "Anthropic Claude (via Claude Code credentials)"
}

// DashboardURL returns the web dashboard URL.
func (p *Provider) DashboardURL() string {
	return "https://console.anthropic.com"
}

// IsConfigured returns true if credentials are available.
func (p *Provider) IsConfigured() bool {
	_, err := p.readCredentials()
	return err == nil
}

// FetchUsage retrieves usage data from Anthropic's API.
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	creds, err := p.readCredentials()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	if creds.IsExpired() {
		// Try auto-refresh using refresh token
		if creds.ClaudeAiOauth.RefreshToken != "" {
			if refreshed, err := p.refreshToken(ctx, creds); err == nil {
				creds = refreshed
			} else {
				return &provider.UsageData{
					Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
					FetchedAt: time.Now(),
					IsExpired: true,
					Error:     "token expired — run `claude` to reauth",
				}, nil
			}
		} else {
			return &provider.UsageData{
				Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "token expired — run `claude` to reauth",
			}, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken())
	req.Header.Set("anthropic-beta", betaHeader)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return &provider.UsageData{
				Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "unauthorized — run `claude` to reauth",
			}, nil
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return &provider.UsageData{
				Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
				FetchedAt: time.Now(),
				Error:     "rate limited (429)",
			}, nil
		}
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp usageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if apiResp.usageUnavailable() {
		return &provider.UsageData{
			Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
			FetchedAt: time.Now(),
			Error:     "usage unavailable",
		}, nil
	}

	data := &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	addUsageWindows(data, apiResp)

	// Extra usage (overage) — only show if enabled
	if apiResp.ExtraUsage != nil && apiResp.ExtraUsage.IsEnabled {
		utilization, ok := apiResp.ExtraUsage.utilization()
		if ok {
			data.Windows = append(data.Windows, provider.UsageWindow{
				Name:        "extra",
				DisplayName: "Extra usage",
				Utilization: utilization,
				Used:        cents(apiResp.ExtraUsage.UsedCredits),
				Limit:       cents(apiResp.ExtraUsage.MonthlyLimit),
			})
		}
	}
	if len(data.Windows) == 0 {
		data.Error = "no complete usage data"
	}

	return data, nil
}

func addUsageWindows(data *provider.UsageData, apiResp usageResponse) {
	seen := make(map[string]bool)
	type namedWindow struct {
		name, display string
		w             *usageWindow
	}
	for _, nw := range []namedWindow{
		{"5h", "5 hours", apiResp.FiveHour},
		{"7d All", "7 days (all models)", apiResp.SevenDay},
		{"7d OAuth", "7 days (OAuth apps)", apiResp.SevenDayOAuthApps},
		{"7d Opus", "7 days (Opus)", apiResp.SevenDayOpus},
		{"7d Sonnet", "7 days (Sonnet)", apiResp.SevenDaySonnet},
		{"bonus", "Bonus", apiResp.IguanaNecktie},
	} {
		if nw.w != nil && validUtilization(nw.w.Utilization) && !nw.w.ResetsAt.IsZero() {
			data.Windows = append(data.Windows, provider.UsageWindow{
				Name:        nw.name,
				DisplayName: nw.display,
				Utilization: *nw.w.Utilization,
				ResetsAt:    nw.w.ResetsAt,
			})
			seen[nw.name] = true
		}
	}
	addLimitWindows(data, apiResp.Limits, seen)
}

func addLimitWindows(data *provider.UsageData, limits []usageLimit, seen map[string]bool) {
	for _, limit := range limits {
		name, display, ok := limitWindowName(limit)
		if !ok || seen[name] || !validUtilization(limit.Percent) || limit.ResetsAt.IsZero() {
			continue
		}
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        name,
			DisplayName: display,
			Utilization: *limit.Percent,
			ResetsAt:    limit.ResetsAt,
		})
		seen[name] = true
	}
}

func limitWindowName(limit usageLimit) (string, string, bool) {
	switch limit.Kind {
	case "session":
		return "5h", "5 hours", true
	case "weekly_all":
		return "7d All", "7 days (all models)", true
	case "weekly_scoped":
		if limit.Scope == nil || limit.Scope.Model == nil || strings.TrimSpace(limit.Scope.Model.DisplayName) == "" {
			return "", "", false
		}
		modelName := strings.TrimSpace(limit.Scope.Model.DisplayName)
		return "7d " + modelName, "7 days (" + modelName + ")", true
	default:
		return "", "", false
	}
}

// Credentials holds OAuth credentials for Anthropic.
type Credentials struct {
	ClaudeAiOauth struct {
		AccessToken      string   `json:"accessToken"`
		RefreshToken     string   `json:"refreshToken"`
		ExpiresAt        int64    `json:"expiresAt"`
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
		RateLimitTier    string   `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`

	tokenOnly   string // set when credentials come from env var or raw keychain value
	persistPath string
}

// AccessToken returns the OAuth access token.
func (c *Credentials) AccessToken() string {
	if c.tokenOnly != "" {
		return c.tokenOnly
	}
	return c.ClaudeAiOauth.AccessToken
}

// IsExpired checks if the token is expired.
func (c *Credentials) IsExpired() bool {
	if c.tokenOnly != "" {
		return false // can't check expiry for raw tokens
	}
	return time.Now().UnixMilli() >= c.ClaudeAiOauth.ExpiresAt
}

// readCredentials tries multiple sources to find credentials.
func (p *Provider) readCredentials() (*Credentials, error) {
	if p.explicitSource {
		return p.readCredentialsFile(p.configDir)
	}
	// 1. Config file (explicit OAuth token)
	if p.cfg.OAuthToken != "" {
		return &Credentials{tokenOnly: p.cfg.OAuthToken}, nil
	}

	// 2. Environment variable (for backward compatibility)
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
			EnvNames: []string{"CLAUDE_CODE_OAUTH_TOKEN"}, AllowSessionEnvironmentFallback: true,
		})
		if token := values["CLAUDE_CODE_OAUTH_TOKEN"]; token != "" {
			return &Credentials{tokenOnly: token}, nil
		}
	} else if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		return &Credentials{tokenOnly: token}, nil
	}

	// 3. macOS Keychain
	if runtime.GOOS == "darwin" {
		if creds, err := p.readKeychain(); err == nil {
			return creds, nil
		}
	}

	// 4. Credentials file (Linux/Claude Code default)
	return p.readCredentialsFile(legacyCredentialsDir())
}

func (p *Provider) readKeychain() (*Credentials, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain: %w", err)
	}
	data := strings.TrimSpace(string(out))
	if data == "" {
		return nil, fmt.Errorf("keychain: empty value")
	}
	var creds Credentials
	if err := json.Unmarshal([]byte(data), &creds); err != nil {
		// Might be a raw token string
		return &Credentials{tokenOnly: data}, nil
	}
	// Keychain values are capabilities, not file ownership. They must never be persisted.
	return &creds, nil
}

func (p *Provider) readCredentialsFile(configDir string) (*Credentials, error) {
	path := filepath.Join(configDir, ".credentials.json")
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credentials: credentials file unavailable")
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: credentials file invalid")
	}
	creds.persistPath = path
	return &creds, nil
}

func legacyCredentialsDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude")
	}
	return filepath.Join(home, ".claude")
}

// refreshToken attempts to refresh the OAuth access token using the refresh token.
func (p *Provider) refreshToken(ctx context.Context, creds *Credentials) (*Credentials, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {creds.ClaudeAiOauth.RefreshToken},
		"client_id":     {oauthClientID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed: HTTP %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access token in refresh response")
	}

	// Update credentials
	creds.ClaudeAiOauth.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		creds.ClaudeAiOauth.RefreshToken = tokenResp.RefreshToken
	}
	creds.ClaudeAiOauth.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UnixMilli()

	// Write back only through the credential source that supplied the token.
	if creds.tokenOnly == "" && creds.persistPath != "" {
		if err := p.writeCredentials(creds); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: warning: failed to save refreshed credentials: %v\n", err)
		}
	}

	return creds, nil
}

// writeCredentials writes updated credentials back to the credentials file.
func (p *Provider) writeCredentials(creds *Credentials) error {
	if creds == nil || creds.tokenOnly != "" {
		return fmt.Errorf("credential source cannot persist")
	}
	path := creds.persistPath
	if path == "" {
		return fmt.Errorf("credential source cannot persist")
	}

	// Read existing file to preserve other fields (e.g., mcpOAuth)
	existing := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("credentials file is not writable")
		}
	}

	// Update the claudeAiOauth section
	oauthData, err := json.Marshal(creds.ClaudeAiOauth)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	existing["claudeAiOauth"] = oauthData

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials file: %w", err)
	}

	// Atomic write
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("credentials file is not writable")
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("credentials file is not writable")
	}
	return nil
}

// Internal API response types.
type usageResponse struct {
	FiveHour          *usageWindow      `json:"five_hour,omitempty"`
	SevenDay          *usageWindow      `json:"seven_day,omitempty"`
	SevenDayOAuthApps *usageWindow      `json:"seven_day_oauth_apps,omitempty"`
	SevenDayOpus      *usageWindow      `json:"seven_day_opus,omitempty"`
	SevenDaySonnet    *usageWindow      `json:"seven_day_sonnet,omitempty"`
	IguanaNecktie     *usageWindow      `json:"iguana_necktie,omitempty"`
	ExtraUsage        *extraUsageWindow `json:"extra_usage,omitempty"`
	Limits            []usageLimit      `json:"limits,omitempty"`
}

func (r usageResponse) usageUnavailable() bool {
	return r.FiveHour != nil &&
		r.SevenDay != nil &&
		validUtilization(r.FiveHour.Utilization) &&
		validUtilization(r.SevenDay.Utilization) &&
		*r.FiveHour.Utilization == 0 &&
		*r.SevenDay.Utilization == 0 &&
		hasResetlessModelWindow(r)
}

func hasResetlessModelWindow(r usageResponse) bool {
	for _, w := range []*usageWindow{
		r.SevenDayOAuthApps,
		r.SevenDayOpus,
		r.SevenDaySonnet,
	} {
		if w != nil && w.ResetsAt.IsZero() {
			return true
		}
	}
	return false
}

type usageWindow struct {
	Utilization *float64  `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

type usageLimit struct {
	Kind     string           `json:"kind"`
	Percent  *float64         `json:"percent"`
	ResetsAt time.Time        `json:"resets_at"`
	Scope    *usageLimitScope `json:"scope"`
}

type usageLimitScope struct {
	Model *usageLimitModelScope `json:"model"`
}

type usageLimitModelScope struct {
	DisplayName string `json:"display_name"`
}

type extraUsageWindow struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit *float64 `json:"monthly_limit"`
	UsedCredits  *float64 `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
	Currency     string   `json:"currency"`
}

func validUtilization(percent *float64) bool {
	return percent != nil && *percent >= 0 && *percent <= 100
}

func (w extraUsageWindow) utilization() (float64, bool) {
	if validUtilization(w.Utilization) {
		return *w.Utilization, true
	}
	if w.MonthlyLimit != nil && w.UsedCredits != nil && *w.MonthlyLimit > 0 && *w.UsedCredits >= 0 {
		percent := *w.UsedCredits / *w.MonthlyLimit * 100
		if percent > 100 {
			percent = 100
		}
		return percent, true
	}
	return 0, false
}

func cents(value *float64) int {
	if value == nil || *value <= 0 {
		return 0
	}
	return int(*value * 100)
}

// Register registers the Anthropic provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("claude")
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}

var _ provider.SourceCapability = (*Provider)(nil)
