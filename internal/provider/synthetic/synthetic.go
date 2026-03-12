// Package synthetic implements the Provider interface for Synthetic (synthetic.new).
package synthetic

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
	quotasURL = "https://api.synthetic.new/v2/quotas"
	timeout   = 10 * time.Second
)

type Provider struct {
	cfg config.ProviderConfig
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string         { return "synthetic" }
func (p *Provider) DisplayName() string  { return "Synthetic" }
func (p *Provider) Description() string  { return "Synthetic (via SYNTHETIC_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://synthetic.new" }

func (p *Provider) IsConfigured() bool {
	_, err := p.getAPIKey()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	apiKey, err := p.getAPIKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", quotasURL, nil)
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
			Error:     "unauthorized — check SYNTHETIC_API_KEY",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.parseQuotas(raw)
}

func (p *Provider) getAPIKey() (string, error) {
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}
	if key := os.Getenv("SYNTHETIC_API_KEY"); key != "" {
		return strings.Trim(key, "\" "), nil
	}
	return "", fmt.Errorf("no API key found")
}

func (p *Provider) parseQuotas(raw json.RawMessage) (*provider.UsageData, error) {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	// Try as array first, then as object with quota arrays
	var entries []map[string]interface{}

	if err := json.Unmarshal(raw, &entries); err != nil {
		// Try as object
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("parse quotas: %w", err)
		}
		// Look for arrays inside the object
		for _, key := range []string{"quotas", "quota", "limits", "usage", "entries", "data"} {
			if arr, ok := obj[key]; ok {
				if b, err := json.Marshal(arr); err == nil {
					if err := json.Unmarshal(b, &entries); err == nil && len(entries) > 0 {
						break
					}
				}
			}
		}
		// If still empty, try nested data.quotas etc.
		if len(entries) == 0 {
			if dataObj, ok := obj["data"].(map[string]interface{}); ok {
				for _, key := range []string{"quotas", "quota", "limits", "usage", "entries"} {
					if arr, ok := dataObj[key]; ok {
						if b, err := json.Marshal(arr); err == nil {
							if err := json.Unmarshal(b, &entries); err == nil && len(entries) > 0 {
								break
							}
						}
					}
				}
			}
		}
		// If still empty, treat the root object as a single quota entry
		if len(entries) == 0 {
			entries = []map[string]interface{}{obj}
		}
	}

	// Parse up to 2 quota entries
	for i, entry := range entries {
		if i >= 2 {
			break
		}
		w := p.parseQuotaEntry(entry, i)
		if w != nil {
			data.Windows = append(data.Windows, *w)
		}
	}

	if len(data.Windows) == 0 {
		data.Error = "no quota data in response"
	}

	return data, nil
}

func (p *Provider) parseQuotaEntry(entry map[string]interface{}, idx int) *provider.UsageWindow {
	// Try direct percent first
	usedPct := provider.FindFloat(entry, []string{"percentUsed", "usedPercent", "usagePercent",
		"usage_percent", "used_percent", "percent_used", "percent"})

	// If <= 1.0, assume it's a fraction
	if usedPct > 0 && usedPct <= 1.0 {
		usedPct *= 100
	}

	// Try inverse percent
	if usedPct == 0 {
		remaining := provider.FindFloat(entry, []string{"percentRemaining", "remainingPercent",
			"remaining_percent", "percent_remaining"})
		if remaining > 0 {
			if remaining <= 1.0 {
				remaining *= 100
			}
			usedPct = 100 - remaining
		}
	}

	// Derive from limit/used/remaining
	if usedPct == 0 {
		limit := provider.FindFloat(entry, []string{"limit", "quota", "max", "total", "capacity", "allowance"})
		used := provider.FindFloat(entry, []string{"used", "usage", "requests", "consumed", "spent"})
		remaining := provider.FindFloat(entry, []string{"remaining", "left", "available", "balance"})

		if limit > 0 && used > 0 {
			usedPct = (used / limit) * 100
		} else if limit > 0 && remaining > 0 {
			usedPct = ((limit - remaining) / limit) * 100
		} else if used > 0 && remaining > 0 {
			usedPct = (used / (used + remaining)) * 100
		}
	}

	if usedPct == 0 {
		return nil
	}

	if usedPct < 0 {
		usedPct = 0
	}
	if usedPct > 100 {
		usedPct = 100
	}

	// Label
	label := findString(entry, []string{"name", "label", "type", "period", "scope", "title"})
	if label == "" {
		label = fmt.Sprintf("quota%d", idx+1)
	}

	// Reset time
	resetsAt := time.Now().Add(24 * time.Hour)
	resetStr := findString(entry, []string{"resetAt", "reset_at", "resetsAt", "resets_at",
		"renewAt", "renew_at", "periodEnd", "period_end", "expiresAt", "expires_at"})
	if resetStr != "" {
		if t, err := time.Parse(time.RFC3339, resetStr); err == nil {
			resetsAt = t
		} else if t, err := time.Parse(time.RFC3339Nano, resetStr); err == nil {
			resetsAt = t
		}
	}
	// Try as epoch
	resetEpoch := provider.FindFloat(entry, []string{"resetAt", "reset_at", "resetsAt", "resets_at",
		"renewAt", "renew_at", "periodEnd", "period_end", "expiresAt", "expires_at"})
	if resetEpoch > 1e12 {
		resetsAt = time.UnixMilli(int64(resetEpoch))
	} else if resetEpoch > 1e9 {
		resetsAt = time.Unix(int64(resetEpoch), 0)
	}

	return &provider.UsageWindow{
		Name:        label,
		DisplayName: label,
		Utilization: usedPct,
		ResetsAt:    resetsAt,
	}
}

func findString(obj map[string]interface{}, keys []string) string {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("synthetic")
	return registry.Register(New(providerCfg))
}
