// Package kimik2 implements the Provider interface for Kimi K2.
package kimik2

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
	creditsURL = "https://kimi-k2.ai/api/user/credits"
	timeout    = 10 * time.Second
)

type Provider struct {
	cfg config.ProviderConfig
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string         { return "kimik2" }
func (p *Provider) DisplayName() string  { return "Kimi K2" }
func (p *Provider) Description() string  { return "Kimi K2 (via KIMI_K2_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://kimi-k2.ai/my-credits" }

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
	req.Header.Set("Accept", "application/json")

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
			Error:     "unauthorized — check KIMI_K2_API_KEY",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	// Parse flexibly — the API shape may vary
	var raw map[string]interface{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	consumed, remaining := p.extractCredits(raw)

	// Also check x-credits-remaining header as fallback
	if remaining == 0 && consumed == 0 {
		if hdr := resp.Header.Get("x-credits-remaining"); hdr != "" {
			fmt.Sscanf(hdr, "%f", &remaining)
		}
	}

	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	total := consumed + remaining
	if total > 0 {
		usedPct := (consumed / total) * 100
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
			ResetsAt:    time.Now().Add(365 * 24 * time.Hour),
		})
	} else {
		data.Error = "no credit data in response"
	}

	return data, nil
}

func (p *Provider) getAPIKey() (string, error) {
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}
	for _, env := range []string{"KIMI_K2_API_KEY", "KIMI_API_KEY", "KIMI_KEY"} {
		if key := os.Getenv(env); key != "" {
			return key, nil
		}
	}
	return "", fmt.Errorf("no API key found")
}

// extractCredits searches for consumed/remaining values in a flexible JSON structure.
func (p *Provider) extractCredits(obj map[string]interface{}) (consumed, remaining float64) {
	// Try root level, then nested under "data", "data.credits", "data.usage", "result", "result.credits"
	sources := []map[string]interface{}{obj}
	for _, key := range []string{"data", "result", "usage", "credits"} {
		if sub, ok := obj[key].(map[string]interface{}); ok {
			sources = append(sources, sub)
			// One more level: data.credits, data.usage, etc.
			for _, k2 := range []string{"credits", "usage"} {
				if sub2, ok := sub[k2].(map[string]interface{}); ok {
					sources = append(sources, sub2)
				}
			}
		}
	}

	consumedKeys := []string{"total_credits_consumed", "totalCreditsConsumed", "total_credits_used",
		"totalCreditsUsed", "credits_consumed", "creditsConsumed", "consumedCredits",
		"usedCredits", "used", "total", "consumed"}
	remainingKeys := []string{"credits_remaining", "creditsRemaining", "remaining_credits",
		"remainingCredits", "available_credits", "availableCredits", "credits_left",
		"creditsLeft", "remaining", "left", "available", "balance"}

	for _, src := range sources {
		if consumed == 0 {
			consumed = provider.FindFloat(src, consumedKeys)
		}
		if remaining == 0 {
			remaining = provider.FindFloat(src, remainingKeys)
		}
	}
	return
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("kimik2")
	return registry.Register(New(providerCfg))
}
