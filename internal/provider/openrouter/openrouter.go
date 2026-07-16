// Package openrouter implements the Provider interface for OpenRouter.
package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	creditsURL = "https://openrouter.ai/api/v1/credits"
	keyURL     = "https://openrouter.ai/api/v1/key"
	timeout    = 10 * time.Second
	maxBody    = 1 << 20
)

type Provider struct {
	cfg                config.ProviderConfig
	client             *http.Client
	creditsURL, keyURL string
}

type apiError int

func (e apiError) Error() string { return fmt.Sprintf("API returned %d", int(e)) }

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: timeout}, creditsURL: creditsURL, keyURL: keyURL}
}
func (p *Provider) Name() string            { return "openrouter" }
func (p *Provider) DisplayName() string     { return "OpenRouter" }
func (p *Provider) Description() string     { return "OpenRouter (via OPENROUTER_API_KEY)" }
func (p *Provider) DashboardURL() string    { return "https://openrouter.ai/credits" }
func (p *Provider) AutoPollByDefault() bool { return false }
func (p *Provider) IsConfigured() bool      { _, err := p.getAPIKey(); return err == nil }

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	key, err := p.getAPIKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	data, err := p.fetchCredits(ctx, key)
	if err != nil {
		if status, ok := err.(apiError); ok && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
			return &provider.UsageData{Provider: p.Name(), FetchedAt: time.Now(), IsExpired: true, Error: "unauthorized — check OPENROUTER_API_KEY"}, nil
		}
		return nil, err
	}
	if keyData, keyErr := p.fetchKey(ctx, key); keyErr == nil {
		applyKey(data, keyData)
	} else {
		data.Warning = "API key limits unavailable: " + keyErr.Error()
	}
	return data, nil
}

func (p *Provider) getAPIKey() (string, error) {
	if strings.TrimSpace(p.cfg.APIKey) != "" {
		return strings.TrimSpace(p.cfg.APIKey), nil
	}
	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("no API key found")
}

type creditsResponse struct {
	Data creditsData `json:"data"`
}
type creditsData struct {
	TotalCredits *float64 `json:"total_credits"`
	TotalUsage   *float64 `json:"total_usage"`
}
type keyResponse struct {
	Data keyData `json:"data"`
}
type keyData struct {
	Limit          *float64 `json:"limit"`
	LimitRemaining *float64 `json:"limit_remaining"`
	Usage          *float64 `json:"usage"`
	LimitReset     string   `json:"limit_reset"`
}

func (p *Provider) request(ctx context.Context, url, key string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
func (p *Provider) fetchCredits(ctx context.Context, key string) (*provider.UsageData, error) {
	var resp creditsResponse
	if err := p.request(ctx, p.creditsURL, key, &resp); err != nil {
		return nil, err
	}
	if resp.Data.TotalCredits == nil || resp.Data.TotalUsage == nil {
		return nil, fmt.Errorf("decode response: missing credits fields")
	}
	total, used := *resp.Data.TotalCredits, *resp.Data.TotalUsage
	remaining := total - used
	if remaining < 0 {
		remaining = 0
	}
	return &provider.UsageData{Provider: p.Name(), FetchedAt: time.Now(), Balances: []provider.UsageBalance{{Name: "credits", DisplayName: "Credits", Total: total, Used: used, Remaining: remaining}}}, nil
}
func (p *Provider) fetchKey(ctx context.Context, key string) (*keyData, error) {
	var resp keyResponse
	if err := p.request(ctx, p.keyURL, key, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}
func applyKey(data *provider.UsageData, k *keyData) {
	if k.Limit == nil || k.LimitRemaining == nil || k.Usage == nil || *k.Limit < 0 {
		return
	}
	used := *k.Usage
	limit := *k.Limit
	if limit == 0 {
		return
	}
	pct := used / limit * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	data.Windows = append(data.Windows, provider.UsageWindow{Name: "key", DisplayName: "API key", Utilization: pct, Limit: int(limit), Used: int(used)})
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("openrouter")
	return registry.Register(New(providerCfg))
}
