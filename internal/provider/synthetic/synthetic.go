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

	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse quotas: %w", err)
	}
	if slots := knownSlots(root); slots != nil {
		labels := [...]string{"Rolling five-hour limit", "Weekly token limit", "Search hourly"}
		for i, slot := range slots {
			if slot == nil {
				continue
			}
			if w := parseQuotaEntry(slot); w != nil {
				w.Name, w.DisplayName = labels[i], labels[i]
				data.Windows = append(data.Windows, *w)
			}
		}
	} else {
		for _, entry := range quotaEntries(root) {
			if w := parseQuotaEntry(entry); w != nil {
				data.Windows = append(data.Windows, *w)
			}
		}
	}

	if len(data.Windows) == 0 {
		data.Error = "no quota data in response"
	}

	return data, nil
}

func knownSlots(root map[string]json.RawMessage) *[3]map[string]json.RawMessage {
	data, _ := object(root["data"])
	slot := func(key string) map[string]json.RawMessage {
		if v, ok := object(root[key]); ok {
			return v
		}
		if v, ok := object(data[key]); ok {
			return v
		}
		return nil
	}
	search, _ := object(root["search"])
	if search == nil {
		search, _ = object(data["search"])
	}
	searchHourly, _ := object(search["hourly"])
	if slot("rollingFiveHourLimit") == nil && slot("weeklyTokenLimit") == nil && searchHourly == nil {
		return nil
	}
	result := [3]map[string]json.RawMessage{slot("rollingFiveHourLimit"), slot("weeklyTokenLimit"), searchHourly}
	return &result
}

func quotaEntries(root map[string]json.RawMessage) []map[string]json.RawMessage {
	for _, key := range []string{"quotas", "quota", "limits", "usage", "entries", "subscription"} {
		if entries, ok := array(root[key]); ok {
			return entries
		}
		if entry, ok := object(root[key]); ok {
			return []map[string]json.RawMessage{entry}
		}
	}
	if data, ok := object(root["data"]); ok {
		return quotaEntries(data)
	}
	return nil
}

func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var value map[string]json.RawMessage
	return value, json.Unmarshal(raw, &value) == nil && value != nil
}

func array(raw json.RawMessage) ([]map[string]json.RawMessage, bool) {
	var value []map[string]json.RawMessage
	return value, len(raw) > 0 && json.Unmarshal(raw, &value) == nil && len(value) > 0
}

func parseQuotaEntry(entry map[string]json.RawMessage) *provider.UsageWindow {
	// Try direct percent first
	usedPct, hasUsedPct := number(entry, []string{"percentUsed", "usedPercent", "usagePercent", "usage_percent", "used_percent", "percent_used", "percent"})

	// If <= 1.0, assume it's a fraction
	if usedPct > 0 && usedPct <= 1.0 {
		usedPct *= 100
	}

	// Try inverse percent
	if !hasUsedPct {
		remaining, ok := number(entry, []string{"percentRemaining", "remainingPercent", "remaining_percent", "percent_remaining"})
		if ok {
			if remaining <= 1.0 {
				remaining *= 100
			}
			usedPct = 100 - remaining
		}
	}

	// Derive from limit/used/remaining
	if !hasUsedPct {
		limit, hasLimit := number(entry, []string{"limit"})
		used, hasUsed := number(entry, []string{"used", "requests"})
		remaining, hasRemaining := number(entry, []string{"remaining"})

		if hasLimit && hasUsed && limit > 0 {
			usedPct = (used / limit) * 100
		} else if hasLimit && hasRemaining && limit > 0 {
			usedPct = ((limit - remaining) / limit) * 100
		} else if hasUsed && hasRemaining && used+remaining > 0 {
			usedPct = (used / (used + remaining)) * 100
		} else {
			return nil
		}
	}

	if usedPct < 0 {
		usedPct = 0
	}
	if usedPct > 100 {
		usedPct = 100
	}

	// Label
	label := findString(entry, []string{"name", "label", "type", "period", "scope", "title"})

	// Reset time
	var resetsAt time.Time
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
	resetEpoch, hasEpoch := number(entry, []string{"resetAt", "reset_at", "resetsAt", "resets_at", "renewAt", "renew_at", "periodEnd", "period_end", "expiresAt", "expires_at"})
	if hasEpoch && resetEpoch > 1e12 {
		resetsAt = time.UnixMilli(int64(resetEpoch))
	} else if hasEpoch && resetEpoch > 1e9 {
		resetsAt = time.Unix(int64(resetEpoch), 0)
	}

	return &provider.UsageWindow{
		Name:        label,
		DisplayName: label,
		Utilization: usedPct,
		ResetsAt:    resetsAt,
	}
}

func number(obj map[string]json.RawMessage, keys []string) (float64, bool) {
	for _, key := range keys {
		var n json.Number
		if err := json.Unmarshal(obj[key], &n); err == nil {
			f, err := n.Float64()
			if err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func findString(obj map[string]json.RawMessage, keys []string) string {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
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
