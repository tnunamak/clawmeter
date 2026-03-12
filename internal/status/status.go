// Package status fetches provider operational status from status pages.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Indicator represents the operational status of a provider.
type Indicator string

const (
	None        Indicator = "none"        // Operational
	Minor       Indicator = "minor"       // Partial outage
	Major       Indicator = "major"       // Major outage
	Critical    Indicator = "critical"    // Critical issue
	Maintenance Indicator = "maintenance" // Under maintenance
	Unknown     Indicator = "unknown"     // Could not determine
)

// HasIssue returns true if the status indicates a known problem.
// Unknown is excluded — it means we couldn't reach the status page, not that there's an issue.
func (i Indicator) HasIssue() bool {
	return i != None && i != Unknown
}

// Label returns a human-readable label for the indicator.
func (i Indicator) Label() string {
	switch i {
	case None:
		return "Operational"
	case Minor:
		return "Partial outage"
	case Major:
		return "Major outage"
	case Critical:
		return "Critical issue"
	case Maintenance:
		return "Maintenance"
	default:
		return "Unknown"
	}
}

// Emoji returns a status emoji.
func (i Indicator) Emoji() string {
	switch i {
	case None:
		return "✓"
	case Minor:
		return "~"
	case Major, Critical:
		return "✗"
	case Maintenance:
		return "⏸"
	default:
		return "?"
	}
}

// ProviderStatus holds the operational status for a provider.
type ProviderStatus struct {
	Indicator   Indicator `json:"indicator"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// statusPageConfig defines how to check a provider's status page.
type statusPageConfig struct {
	BaseURL    string
	Components []string // component names to monitor (empty = use overall status)
}

// StatusPages maps provider names to their status page configuration.
var StatusPages = map[string]statusPageConfig{
	"claude": {
		BaseURL:    "https://status.anthropic.com",
		Components: []string{"Claude API (api.anthropic.com)", "Claude Code"},
	},
	"openai": {
		BaseURL:    "https://status.openai.com",
		Components: []string{"Chat Completions", "Codex", "Responses"},
	},
	"copilot": {
		BaseURL:    "https://www.githubstatus.com",
		Components: []string{"Copilot"},
	},
	"openrouter": {
		BaseURL: "https://status.openrouter.ai",
	},
}

// componentsResponse is the statuspage.io components API response.
type componentsResponse struct {
	Components []componentEntry `json:"components"`
	Page       struct {
		UpdatedAt string `json:"updated_at"`
	} `json:"page"`
}

type componentEntry struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

// statuspageResponse is the statuspage.io overall status API response (fallback).
type statuspageResponse struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Page struct {
		UpdatedAt string `json:"updated_at"`
	} `json:"page"`
}

// Fetch retrieves the operational status for a provider.
// Returns nil if no status page is configured for the provider.
func Fetch(ctx context.Context, providerName string) *ProviderStatus {
	cfg, ok := StatusPages[providerName]
	if !ok {
		return nil
	}
	if len(cfg.Components) > 0 {
		return fetchComponents(ctx, cfg)
	}
	return fetchStatuspage(ctx, cfg.BaseURL)
}

// FetchAll retrieves status only for the given provider names that have status pages.
func FetchAll(ctx context.Context, providerNames []string) map[string]*ProviderStatus {
	var toFetch []string
	for _, name := range providerNames {
		if _, ok := StatusPages[name]; ok {
			toFetch = append(toFetch, name)
		}
	}

	if len(toFetch) == 0 {
		return nil
	}

	results := make(map[string]*ProviderStatus, len(toFetch))
	type result struct {
		name   string
		status *ProviderStatus
	}
	ch := make(chan result, len(toFetch))

	for _, name := range toFetch {
		go func(n string) {
			ch <- result{name: n, status: Fetch(ctx, n)}
		}(name)
	}

	for i := 0; i < len(toFetch); i++ {
		r := <-ch
		if r.status != nil {
			results[r.name] = r.status
		}
	}
	return results
}

var componentStatusWeight = map[string]int{
	"operational":          0,
	"under_maintenance":    1,
	"degraded_performance": 2,
	"partial_outage":       3,
	"major_outage":         4,
}

func fetchComponents(ctx context.Context, cfg statusPageConfig) *ProviderStatus {
	url := cfg.BaseURL + "/api/v2/components.json"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &ProviderStatus{Indicator: Unknown}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ProviderStatus{Indicator: Unknown}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fetchStatuspage(ctx, cfg.BaseURL)
	}

	var apiResp componentsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return &ProviderStatus{Indicator: Unknown}
	}

	watched := make(map[string]bool, len(cfg.Components))
	for _, name := range cfg.Components {
		watched[name] = true
	}

	var worstWeight int
	var worstStatus string
	var worstName string
	var updatedAt time.Time

	for _, c := range apiResp.Components {
		if !watched[c.Name] {
			continue
		}
		w := componentStatusWeight[c.Status]
		if w > worstWeight {
			worstWeight = w
			worstStatus = c.Status
			worstName = c.Name
			if c.UpdatedAt != "" {
				if t, err := time.Parse(time.RFC3339, c.UpdatedAt); err == nil {
					updatedAt = t
				} else if t, err := time.Parse(time.RFC3339Nano, c.UpdatedAt); err == nil {
					updatedAt = t
				}
			}
		}
	}

	if worstWeight == 0 {
		return &ProviderStatus{Indicator: None}
	}

	indicator := componentStatusToIndicator(worstStatus)
	desc := fmt.Sprintf("%s: %s", worstName, indicator.Label())

	return &ProviderStatus{
		Indicator:   indicator,
		Description: desc,
		UpdatedAt:   updatedAt,
	}
}

func componentStatusToIndicator(status string) Indicator {
	switch status {
	case "degraded_performance", "partial_outage":
		return Minor
	case "major_outage":
		return Major
	case "under_maintenance":
		return Maintenance
	default:
		return None
	}
}

func fetchStatuspage(ctx context.Context, baseURL string) *ProviderStatus {
	url := baseURL + "/api/v2/status.json"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &ProviderStatus{Indicator: Unknown}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ProviderStatus{Indicator: Unknown}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &ProviderStatus{Indicator: Unknown}
	}

	var apiResp statuspageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return &ProviderStatus{Indicator: Unknown}
	}

	indicator := parseIndicator(apiResp.Status.Indicator)

	var updatedAt time.Time
	if apiResp.Page.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, apiResp.Page.UpdatedAt); err == nil {
			updatedAt = t
		} else if t, err := time.Parse(time.RFC3339Nano, apiResp.Page.UpdatedAt); err == nil {
			updatedAt = t
		}
	}

	return &ProviderStatus{
		Indicator:   indicator,
		Description: apiResp.Status.Description,
		UpdatedAt:   updatedAt,
	}
}

func parseIndicator(s string) Indicator {
	switch s {
	case "none":
		return None
	case "minor":
		return Minor
	case "major":
		return Major
	case "critical":
		return Critical
	case "maintenance":
		return Maintenance
	default:
		return Unknown
	}
}

// FormatCLI returns a short colored status string for CLI output.
func (ps *ProviderStatus) FormatCLI() string {
	if ps == nil {
		return ""
	}
	if !ps.Indicator.HasIssue() {
		return ""
	}
	label := ps.Indicator.Label()
	if ps.Description != "" {
		label = ps.Description
	}
	switch ps.Indicator {
	case Major, Critical:
		return fmt.Sprintf("\033[31m%s %s\033[0m", ps.Indicator.Emoji(), label)
	case Minor:
		return fmt.Sprintf("\033[33m%s %s\033[0m", ps.Indicator.Emoji(), label)
	case Maintenance:
		return fmt.Sprintf("\033[36m%s %s\033[0m", ps.Indicator.Emoji(), label)
	default:
		return label
	}
}
