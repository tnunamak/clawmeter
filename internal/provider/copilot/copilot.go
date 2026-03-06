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

func (p *Provider) Name() string        { return "copilot" }
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

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Editor-Version", "vscode/1.96.2")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	req.Header.Set("X-Github-Api-Version", "2025-04-01")

	client := &http.Client{Timeout: timeout}
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
			Error:     "unauthorized — check GITHUB_TOKEN or gh auth login",
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

	// 2. Environment variables
	if token := os.Getenv("COPILOT_API_TOKEN"); token != "" {
		return token, nil
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// 3. GitHub Copilot hosts.json
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	hostsPath := filepath.Join(home, ".config", "github-copilot", "hosts.json")
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return "", fmt.Errorf("no copilot credentials found")
	}

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
	QuotaSnapshots map[string]quotaSnapshot `json:"quotaSnapshots"`
}

type quotaSnapshot struct {
	PercentRemaining float64 `json:"percentRemaining"`
}

func (p *Provider) transformUsage(resp *userResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	if snap, ok := resp.QuotaSnapshots["premiumInteractions"]; ok {
		usedPct := clamp(100-snap.PercentRemaining, 0, 100)
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "premium",
			DisplayName: "Premium",
			Utilization: usedPct,
			ResetsAt:    time.Now().Add(30 * 24 * time.Hour), // monthly reset
		})
	}

	if snap, ok := resp.QuotaSnapshots["chat"]; ok {
		usedPct := clamp(100-snap.PercentRemaining, 0, 100)
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "chat",
			DisplayName: "Chat",
			Utilization: usedPct,
			ResetsAt:    time.Now().Add(30 * 24 * time.Hour),
		})
	}

	if len(data.Windows) == 0 {
		data.Error = "no quota data in response"
	}

	return data
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
