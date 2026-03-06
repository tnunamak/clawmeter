// Package zai implements the Provider interface for z.ai (Zhipu AI / GLM).
package zai

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
	defaultBaseURL = "https://api.z.ai"
	quotaPath      = "/api/monitor/usage/quota/limit"
	timeout        = 10 * time.Second
)

type Provider struct {
	cfg config.ProviderConfig
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string        { return "zai" }
func (p *Provider) DisplayName() string  { return "z.ai" }
func (p *Provider) Description() string  { return "Zhipu AI / GLM (via Z_AI_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://z.ai/manage-apikey/subscription" }

func (p *Provider) IsConfigured() bool {
	_, err := p.getAPIKey()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	apiKey, err := p.getAPIKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	url := p.getQuotaURL()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+apiKey)
	req.Header.Set("accept", "application/json")

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
			Error:     "unauthorized — check Z_AI_API_KEY",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success || apiResp.Code != 200 {
		return nil, fmt.Errorf("API error: %s", apiResp.Msg)
	}

	return p.transformLimits(&apiResp), nil
}

func (p *Provider) getAPIKey() (string, error) {
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}
	if key := os.Getenv("Z_AI_API_KEY"); key != "" {
		return strings.Trim(key, "\" "), nil
	}
	return "", fmt.Errorf("no API key found")
}

func (p *Provider) getQuotaURL() string {
	// Full URL override
	if url := os.Getenv("Z_AI_QUOTA_URL"); url != "" {
		if !strings.HasPrefix(url, "http") {
			url = "https://" + url
		}
		return url
	}
	// Host override
	if host := os.Getenv("Z_AI_API_HOST"); host != "" {
		if !strings.HasPrefix(host, "http") {
			host = "https://" + host
		}
		host = strings.TrimRight(host, "/")
		return host + quotaPath
	}
	return defaultBaseURL + quotaPath
}

// API response types

type apiResponse struct {
	Code    int      `json:"code"`
	Msg     string   `json:"msg"`
	Success bool     `json:"success"`
	Data    apiData  `json:"data"`
}

type apiData struct {
	Limits   []apiLimit `json:"limits"`
	PlanName string     `json:"planName"`
}

type apiLimit struct {
	Type          string  `json:"type"`           // "TOKENS_LIMIT" or "TIME_LIMIT"
	Unit          int     `json:"unit"`            // 0=unknown, 1=days, 3=hours, 5=minutes
	Number        int     `json:"number"`          // multiplier for unit
	Usage         *int64  `json:"usage"`           // total limit
	CurrentValue  *int64  `json:"currentValue"`    // amount used
	Remaining     *int64  `json:"remaining"`
	Percentage    int     `json:"percentage"`       // 0-100 fallback
	NextResetTime int64   `json:"nextResetTime"`   // milliseconds epoch
}

func (p *Provider) transformLimits(resp *apiResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	for _, limit := range resp.Data.Limits {
		name := "tokens"
		displayName := "Tokens"
		if limit.Type == "TIME_LIMIT" {
			name = "time"
			displayName = "Time"
		}

		// Compute utilization
		var usedPct float64
		if limit.Usage != nil && *limit.Usage > 0 && limit.CurrentValue != nil {
			usedPct = float64(*limit.CurrentValue) / float64(*limit.Usage) * 100
		} else {
			usedPct = float64(limit.Percentage)
		}
		if usedPct < 0 {
			usedPct = 0
		}
		if usedPct > 100 {
			usedPct = 100
		}

		// Parse reset time (milliseconds epoch)
		resetsAt := time.Now().Add(24 * time.Hour)
		if limit.NextResetTime > 0 {
			resetsAt = time.UnixMilli(limit.NextResetTime)
		}

		// Add window duration to display name
		windowDesc := unitToString(limit.Unit, limit.Number)
		if windowDesc != "" {
			displayName += " (" + windowDesc + ")"
		}

		var used, total int
		if limit.CurrentValue != nil {
			used = int(*limit.CurrentValue)
		}
		if limit.Usage != nil {
			total = int(*limit.Usage)
		}

		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        name,
			DisplayName: displayName,
			Utilization: usedPct,
			ResetsAt:    resetsAt,
			Limit:       total,
			Used:        used,
		})
	}

	return data
}

func unitToString(unit, number int) string {
	switch unit {
	case 1:
		if number == 1 {
			return "daily"
		}
		return fmt.Sprintf("%dd", number)
	case 3:
		if number == 1 {
			return "hourly"
		}
		return fmt.Sprintf("%dh", number)
	case 5:
		return fmt.Sprintf("%dm", number)
	default:
		return ""
	}
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("zai")
	return registry.Register(New(providerCfg))
}
