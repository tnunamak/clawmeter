// Package openrouter implements the Provider interface for OpenRouter.
package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	creditsURL = "https://openrouter.ai/api/v1/credits"
	timeout    = 10 * time.Second
)

// Provider implements the provider.Provider interface for OpenRouter.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new OpenRouter provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func (p *Provider) Name() string         { return "openrouter" }
func (p *Provider) DisplayName() string  { return "OpenRouter" }
func (p *Provider) Description() string  { return "OpenRouter (via OPENROUTER_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://openrouter.ai/credits" }

func (p *Provider) IsConfigured() bool {
	_, err := p.getAPIKey()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	apiKey, err := p.getAPIKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", creditsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
			Error:     "unauthorized — check OPENROUTER_API_KEY",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp creditsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.transformCredits(&apiResp), nil
}

func (p *Provider) getAPIKey() (string, error) {
	// 1. Config API key
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}

	// 2. Environment variable
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		return key, nil
	}

	return "", fmt.Errorf("no API key found")
}

// API response types

type creditsResponse struct {
	TotalCredits float64 `json:"total_credits"`
	Usage        float64 `json:"usage"`
}

func (p *Provider) transformCredits(resp *creditsResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	if resp.TotalCredits > 0 {
		usedPct := (resp.Usage / resp.TotalCredits) * 100
		if usedPct < 0 {
			usedPct = 0
		}
		if usedPct > 100 {
			usedPct = 100
		}
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "credits",
			DisplayName: "Credits",
			Utilization: usedPct,
			// Credits don't reset — use a far-future time so forecasting doesn't flag it
			ResetsAt: time.Now().Add(365 * 24 * time.Hour),
		})
	} else {
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "credits",
			DisplayName: "Credits",
			Utilization: 0,
			ResetsAt:    time.Now().Add(365 * 24 * time.Hour),
		})
	}

	return data
}

// Register registers the OpenRouter provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("openrouter")
	return registry.Register(New(providerCfg))
}
