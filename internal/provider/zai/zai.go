// Package zai implements the Provider interface for z.ai (Zhipu AI / GLM).
package zai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	defaultBaseURL = "https://api.z.ai"
	quotaPath      = "/api/monitor/usage/quota/limit"
	timeout        = 10 * time.Second
	maxBodySize    = 1 << 20
)

type Provider struct {
	cfg    config.ProviderConfig
	client *http.Client
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: timeout}}
}

func (p *Provider) Name() string         { return "zai" }
func (p *Provider) DisplayName() string  { return "z.ai" }
func (p *Provider) Description() string  { return "Zhipu AI / GLM (via Z_AI_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://z.ai/manage-apikey/subscription" }
func (p *Provider) AutoPollByDefault() bool {
	return false
}

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

	resp, err := p.client.Do(req)
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
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodySize))
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodySize)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success || apiResp.Code != 200 {
		return nil, fmt.Errorf("API error code %d", apiResp.Code)
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
	if raw := strings.TrimSpace(os.Getenv("Z_AI_QUOTA_URL")); raw != "" {
		if endpoint, ok := safeEndpoint(raw); ok {
			return endpoint
		}
		return ""
	}
	if raw := strings.TrimSpace(os.Getenv("Z_AI_API_HOST")); raw != "" {
		if endpoint, ok := safeEndpoint(raw); ok {
			return strings.TrimRight(endpoint, "/") + quotaPath
		}
		return ""
	}
	base := defaultBaseURL
	if strings.EqualFold(os.Getenv("Z_AI_REGION"), "cn") {
		base = "https://open.bigmodel.cn"
	}
	return base + quotaPath
}

func safeEndpoint(raw string) (string, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), "\"'")
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return u.String(), true
}

// API response types

type apiResponse struct {
	Code    int     `json:"code"`
	Msg     string  `json:"msg"`
	Success bool    `json:"success"`
	Data    apiData `json:"data"`
}

type apiData struct {
	Limits   []apiLimit `json:"limits"`
	PlanName string     `json:"planName"`
}

type apiLimit struct {
	Type          string `json:"type"`         // "TOKENS_LIMIT" or "TIME_LIMIT"
	Unit          int    `json:"unit"`         // 0=unknown, 1=days, 3=hours, 5=minutes
	Number        int    `json:"number"`       // multiplier for unit
	Usage         *int64 `json:"usage"`        // total limit
	CurrentValue  *int64 `json:"currentValue"` // amount used
	Remaining     *int64 `json:"remaining"`
	Percentage    *int   `json:"percentage"`    // 0-100 fallback
	NextResetTime *int64 `json:"nextResetTime"` // milliseconds epoch; absent means unknown
}

func (p *Provider) transformLimits(resp *apiResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	tokenIndex := 0
	for _, limit := range resp.Data.Limits {
		name := "time"
		displayName := "Tokens"
		if limit.Type == "TIME_LIMIT" {
			displayName = "Time"
		} else if limit.Type == "TOKENS_LIMIT" {
			tokenIndex++
			name = tokenWindowName(limit, tokenIndex)
		} else {
			continue
		}

		// Compute utilization
		used, total := limitUsage(limit)
		var usedPct float64
		if total > 0 {
			usedPct = float64(used) / float64(total) * 100
		} else if limit.Percentage != nil {
			usedPct = float64(*limit.Percentage)
		} else {
			continue
		}
		if usedPct < 0 {
			usedPct = 0
		}
		if usedPct > 100 {
			usedPct = 100
		}

		// Parse reset time (milliseconds epoch)
		var resetsAt time.Time
		if limit.NextResetTime != nil && *limit.NextResetTime > 0 {
			resetsAt = time.UnixMilli(*limit.NextResetTime)
		}

		// Add window duration to display name
		windowDesc := unitToString(limit.Unit, limit.Number)
		if windowDesc != "" {
			displayName += " (" + windowDesc + ")"
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
	if len(data.Windows) == 0 {
		data.Error = "no complete quota data"
	}

	return data
}

// limitUsage follows CodexBar's contradiction-safe rule: when direct usage and
// remaining-derived usage disagree, retain the larger non-negative estimate.
func limitUsage(limit apiLimit) (used, total int) {
	if limit.Usage == nil || *limit.Usage <= 0 {
		return nonNegativeInt(pointerValue(limit.CurrentValue)), 0
	}
	total = int(*limit.Usage)
	current := nonNegativeInt(pointerValue(limit.CurrentValue))
	derived := 0
	if limit.Remaining != nil {
		derived = int(*limit.Usage - *limit.Remaining)
		if derived < 0 {
			derived = 0
		}
	}
	if derived > current {
		current = derived
	}
	if current > total {
		current = total
	}
	return current, total
}

func pointerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func nonNegativeInt(value int64) int {
	if value <= 0 {
		return 0
	}
	return int(value)
}

func tokenWindowName(limit apiLimit, index int) string {
	if limit.Unit == 6 && limit.Number == 1 {
		return "tokens_weekly"
	}
	if limit.Unit == 3 && limit.Number == 5 {
		return "tokens_5h"
	}
	return "tokens_" + strconv.Itoa(index)
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
	case 6:
		if number == 1 {
			return "weekly"
		}
		return fmt.Sprintf("%dw", number)
	default:
		return ""
	}
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("zai")
	return registry.Register(New(providerCfg))
}
