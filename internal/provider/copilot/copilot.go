// Package copilot implements the Provider interface for GitHub Copilot.
package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	apiURL  = "https://api.github.com/copilot_internal/user"
	timeout = 10 * time.Second
)

// Provider implements the provider.Provider interface for GitHub Copilot.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new Copilot provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func (p *Provider) Name() string         { return "copilot" }
func (p *Provider) DisplayName() string  { return "Copilot" }
func (p *Provider) Description() string  { return "GitHub Copilot (via GitHub token)" }
func (p *Provider) DashboardURL() string { return "https://github.com/settings/copilot" }

func (p *Provider) IsConfigured() bool {
	_, err := p.getToken()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	token, err := p.getToken()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	return p.fetchUsage(ctx, &http.Client{Timeout: timeout}, apiURL, token)
}

func (p *Provider) fetchUsage(ctx context.Context, client *http.Client, endpoint, token string) (*provider.UsageData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Editor-Version", "vscode/1.96.2")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	req.Header.Set("X-Github-Api-Version", "2025-04-01")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &provider.UsageData{
			Provider:  p.Name(),
			FetchedAt: time.Now(),
			IsExpired: true,
			Error:     "no active subscription — enable at github.com/settings/copilot",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp userResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.transformUsage(&apiResp), nil
}

func (p *Provider) getToken() (string, error) {
	// 1. Config API key
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}

	// 2. Copilot-specific env var (not GITHUB_TOKEN — a generic GitHub
	// token doesn't grant Copilot API access and causes false positives)
	if token := os.Getenv("COPILOT_API_TOKEN"); token != "" {
		return token, nil
	}

	// 3. GitHub Copilot hosts.json (VS Code extension credential store)
	for _, path := range copilotHostsPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return tokenFromHostsJSON(data)
	}

	return "", fmt.Errorf("no copilot credentials found")
}

// copilotHostsPaths returns platform-specific paths for the Copilot hosts.json file.
func copilotHostsPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	if runtime.GOOS == "windows" {
		var paths []string
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "github-copilot", "hosts.json"))
		}
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "github-copilot", "hosts.json"))
		}
		return paths
	}

	return []string{filepath.Join(home, ".config", "github-copilot", "hosts.json")}
}

func tokenFromHostsJSON(data []byte) (string, error) {

	var hosts map[string]struct {
		OAuthToken string `json:"oauth_token"`
	}
	if err := json.Unmarshal(data, &hosts); err != nil {
		return "", fmt.Errorf("parse hosts.json: %w", err)
	}

	if h, ok := hosts["github.com"]; ok && h.OAuthToken != "" {
		return h.OAuthToken, nil
	}

	return "", fmt.Errorf("no github.com token in hosts.json")
}

// API response types

type userResponse struct {
	QuotaSnapshots  map[string]quotaSnapshot `json:"quotaSnapshots"`
	QuotaSnapshots2 map[string]quotaSnapshot `json:"quota_snapshots"`
	QuotaResetDate  string                   `json:"quotaResetDate"`
	QuotaResetDate2 string                   `json:"quota_reset_date"`
}

type quotaSnapshot struct {
	PercentRemaining *float64 `json:"percentRemaining"`
}

func (p *Provider) transformUsage(resp *userResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}
	resetAt, resetKnown := parseResetDate(resp.QuotaResetDate)
	if !resetKnown {
		resetAt, resetKnown = parseResetDate(resp.QuotaResetDate2)
	}
	if !resetKnown && (resp.QuotaResetDate != "" || resp.QuotaResetDate2 != "") {
		data.Warning = "Copilot returned an invalid quota reset date; reset is unknown"
	}
	snapshots := resp.QuotaSnapshots
	if len(snapshots) == 0 {
		snapshots = resp.QuotaSnapshots2
	}

	if snap, ok := snapshots["premiumInteractions"]; ok && validPercentRemaining(snap.PercentRemaining) {
		usedPct := clamp(100-*snap.PercentRemaining, 0, 100)
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "premium",
			DisplayName: "Premium",
			Utilization: usedPct,
			ResetsAt:    resetAt,
		})
	}

	if snap, ok := snapshots["chat"]; ok && validPercentRemaining(snap.PercentRemaining) {
		usedPct := clamp(100-*snap.PercentRemaining, 0, 100)
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "chat",
			DisplayName: "Chat",
			Utilization: usedPct,
			ResetsAt:    resetAt,
		})
	}

	if len(data.Windows) == 0 {
		data.Error = "no quota data in response"
	}
	if !resetKnown && len(data.Windows) > 0 && data.Warning == "" {
		data.Warning = "Copilot quota reset date is not available; reset is unknown"
	}

	return data
}

func validPercentRemaining(percent *float64) bool {
	return percent != nil && *percent >= 0 && *percent <= 100
}

func parseResetDate(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Register registers the Copilot provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("copilot")
	return registry.Register(New(providerCfg))
}
