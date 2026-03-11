// Package anthropic implements the Provider interface for Anthropic/Claude.
package anthropic

import (
	"context"
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
	usageURL        = "https://api.anthropic.com/api/oauth/usage"
	tokenURL        = "https://platform.claude.com/v1/oauth/token"
	oauthClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	betaHeader      = "oauth-2025-04-20"
	timeout         = 15 * time.Second
)

// Provider implements the provider.Provider interface for Anthropic/Claude.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new Anthropic provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
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
					Provider:  p.Name(),
					FetchedAt: time.Now(),
					IsExpired: true,
					Error:     "token expired — run `claude` to reauth",
				}, nil
			}
		} else {
			return &provider.UsageData{
				Provider:  p.Name(),
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
				Provider:  p.Name(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "unauthorized — run `claude` to reauth",
			}, nil
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return &provider.UsageData{
				Provider:  p.Name(),
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

	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	// Add all non-nil windows
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
		if nw.w != nil && nw.w.Utilization >= 0 {
			data.Windows = append(data.Windows, provider.UsageWindow{
				Name:        nw.name,
				DisplayName: nw.display,
				Utilization: nw.w.Utilization,
				ResetsAt:    nw.w.ResetsAt,
			})
		}
	}

	// Extra usage (overage) — only show if enabled
	if apiResp.ExtraUsage != nil && apiResp.ExtraUsage.IsEnabled {
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "extra",
			DisplayName: "Extra usage",
			Utilization: apiResp.ExtraUsage.Utilization,
			Used:        int(apiResp.ExtraUsage.UsedCredits * 100), // cents
			Limit:       int(apiResp.ExtraUsage.MonthlyLimit * 100),
		})
	}

	return data, nil
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

	tokenOnly string // set when credentials come from env var or raw keychain value
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
	// 1. Config file (explicit OAuth token)
	if p.cfg.OAuthToken != "" {
		return &Credentials{tokenOnly: p.cfg.OAuthToken}, nil
	}

	// 2. Environment variable (for backward compatibility)
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		return &Credentials{tokenOnly: token}, nil
	}

	// 3. macOS Keychain
	if runtime.GOOS == "darwin" {
		if creds, err := p.readKeychain(); err == nil {
			return creds, nil
		}
	}

	// 4. Credentials file (Linux/Claude Code default)
	return p.readCredentialsFile()
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
	return &creds, nil
}

func (p *Provider) readCredentialsFile() (*Credentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return &creds, nil
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

	// Write back to credentials file
	if err := p.writeCredentials(creds); err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: warning: failed to save refreshed credentials: %v\n", err)
	}

	return creds, nil
}

// writeCredentials writes updated credentials back to the credentials file.
func (p *Provider) writeCredentials(creds *Credentials) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	path := filepath.Join(home, ".claude", ".credentials.json")

	// Read existing file to preserve other fields (e.g., mcpOAuth)
	existing := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parse existing credentials: %w", err)
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
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename credentials: %w", err)
	}
	return nil
}

// Internal API response types.
type usageResponse struct {
	FiveHour         *usageWindow `json:"five_hour,omitempty"`
	SevenDay         *usageWindow `json:"seven_day,omitempty"`
	SevenDayOAuthApps *usageWindow `json:"seven_day_oauth_apps,omitempty"`
	SevenDayOpus     *usageWindow `json:"seven_day_opus,omitempty"`
	SevenDaySonnet   *usageWindow `json:"seven_day_sonnet,omitempty"`
	IguanaNecktie    *usageWindow `json:"iguana_necktie,omitempty"`
	ExtraUsage       *extraUsageWindow `json:"extra_usage,omitempty"`
}

type usageWindow struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

type extraUsageWindow struct {
	IsEnabled   bool    `json:"is_enabled"`
	MonthlyLimit float64 `json:"monthly_limit"`
	UsedCredits  float64 `json:"used_credits"`
	Utilization  float64 `json:"utilization"`
	Currency     string  `json:"currency"`
}

// Register registers the Anthropic provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("claude")
	return registry.Register(New(providerCfg))
}
