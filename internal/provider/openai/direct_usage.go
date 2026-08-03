package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	directUsagePath    = "/backend-api/wham/usage"
	directUsageTimeout = 10 * time.Second
)

var (
	directUsageURL        = "https://chatgpt.com" + directUsagePath
	directUsageHTTPClient = &http.Client{Timeout: directUsageTimeout}
)

// fetchUsageDirect reads the same authenticated Codex quota surface used by
// the desktop client. It is a fail-soft fallback for a missing or unhealthy
// local CLI; it never mutates quota state.
func (p *Provider) fetchUsageDirect(ctx context.Context, auth *authFile) (*provider.UsageData, error) {
	accessToken, accountID, ok := resetCreditAuth(auth)
	if !ok {
		return nil, fmt.Errorf("direct Codex quota read requires ChatGPT authentication")
	}

	reqCtx, cancel := context.WithTimeout(ctx, directUsageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, directUsageURL, nil)
	if err != nil {
		return nil, err
	}
	if strings.Contains(req.URL.Path, "/consume") {
		return nil, fmt.Errorf("refusing Codex usage consume URL")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	req.Header.Set("Originator", "Codex Desktop")
	req.Header.Set("OAI-Product-Sku", "CODEX")
	req.Header.Set("Accept", "application/json")

	resp, err := directUsageHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("direct Codex quota request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("direct Codex quota request: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read direct Codex quota response: %w", err)
	}
	return p.parseDirectUsage(body, time.Now())
}

func (p *Provider) parseDirectUsage(body []byte, now time.Time) (*provider.UsageData, error) {
	var response struct {
		RateLimit *struct {
			PrimaryWindow *directUsageWindow `json:"primary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse direct Codex quota response: %w", err)
	}

	result := &provider.UsageData{Provider: p.Name(), FetchedAt: now}
	if !validDirectUsageWindow(response.RateLimit) {
		result.Error = "no complete rate limit data"
		return result, nil
	}

	window := response.RateLimit.PrimaryWindow
	resetAt := time.Unix(window.ResetAt, 0)
	name, displayName := directWindowLabels(window.LimitWindowSeconds, resetAt, now)
	result.Windows = []provider.UsageWindow{{
		Name:        name,
		DisplayName: displayName,
		Utilization: *window.UsedPercent,
		ResetsAt:    resetAt,
	}}
	return result, nil
}

type directUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

func validDirectUsageWindow(rateLimit *struct {
	PrimaryWindow *directUsageWindow `json:"primary_window"`
}) bool {
	if rateLimit == nil || rateLimit.PrimaryWindow == nil {
		return false
	}
	window := rateLimit.PrimaryWindow
	return window.UsedPercent != nil && *window.UsedPercent >= 0 && *window.UsedPercent <= 100 && window.ResetAt > 0
}

func directWindowLabels(windowSeconds int64, resetsAt, now time.Time) (string, string) {
	if windowSeconds >= int64(24*time.Hour/time.Second) {
		return "7d", "7 days"
	}
	return primaryWindowLabels(resetsAt, now)
}
