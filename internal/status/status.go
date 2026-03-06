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

// Known status page base URLs (statuspage.io format).
var StatusPages = map[string]string{
	"claude":     "https://status.anthropic.com",
	"openai":     "https://status.openai.com",
	"copilot":    "https://www.githubstatus.com",
	"openrouter": "https://status.openrouter.ai",
}

// statuspageResponse is the statuspage.io API response structure.
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
	baseURL, ok := StatusPages[providerName]
	if !ok {
		return nil
	}
	return fetchStatuspage(ctx, baseURL)
}

// FetchAll retrieves status only for the given provider names that have status pages.
func FetchAll(ctx context.Context, providerNames []string) map[string]*ProviderStatus {
	// Only fetch for providers that have a status page
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
	switch ps.Indicator {
	case Major, Critical:
		return fmt.Sprintf("\033[31m%s %s\033[0m", ps.Indicator.Emoji(), ps.Indicator.Label())
	case Minor:
		return fmt.Sprintf("\033[33m%s %s\033[0m", ps.Indicator.Emoji(), ps.Indicator.Label())
	case Maintenance:
		return fmt.Sprintf("\033[36m%s %s\033[0m", ps.Indicator.Emoji(), ps.Indicator.Label())
	default:
		return ps.Indicator.Label()
	}
}
