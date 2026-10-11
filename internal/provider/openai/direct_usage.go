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
	directUsageHTTPClient = newReadOnlyHTTPClient(directUsageTimeout)
)

func newReadOnlyHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

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
	if err := provider.RateLimitFromResponse(resp); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("direct Codex quota request: %w", &provider.HTTPError{StatusCode: resp.StatusCode})
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read direct Codex quota response: %w", err)
	}
	return p.parseDirectUsage(body, time.Now())
}

func (p *Provider) parseDirectUsage(body []byte, now time.Time) (*provider.UsageData, error) {
	var response struct {
		RateLimit           *directRateLimit `json:"rate_limit"`
		CodeReviewRateLimit *directRateLimit `json:"code_review_rate_limit"`
		AdditionalLimits    []struct {
			LimitName      string           `json:"limit_name"`
			MeteredFeature string           `json:"metered_feature"`
			RateLimit      *directRateLimit `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse direct Codex quota response: %w", err)
	}

	result := &provider.UsageData{Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(), FetchedAt: now}
	windows := appendLimitWindows(nil, "", response.RateLimit.limit(), now)
	windows = appendLimitWindows(windows, "Review", response.CodeReviewRateLimit.limit(), now)
	rawNames := make([]string, len(response.AdditionalLimits))
	ids := make([]string, len(response.AdditionalLimits))
	for i, extra := range response.AdditionalLimits {
		ids[i] = extra.MeteredFeature
		rawNames[i] = extra.LimitName
		if strings.TrimSpace(rawNames[i]) == "" {
			rawNames[i] = extra.MeteredFeature
		}
	}
	for i, label := range resolveScopeLabels(rawNames, ids) {
		windows = appendLimitWindows(windows, label, response.AdditionalLimits[i].RateLimit.limit(), now)
	}
	if len(windows) == 0 {
		result.Error = "no complete rate limit data"
		return result, nil
	}
	result.Windows = windows
	return result, nil
}

// directRateLimit is the `rate_limit`-shaped object the wham usage endpoint
// uses for the main, code-review, and additional limits.
type directRateLimit struct {
	Allowed         *bool              `json:"allowed"`
	LimitReached    bool               `json:"limit_reached"`
	PrimaryWindow   *directUsageWindow `json:"primary_window"`
	SecondaryWindow *directUsageWindow `json:"secondary_window"`
}

func (r *directRateLimit) limit() *codexLimit {
	if r == nil {
		return nil
	}
	return &codexLimit{
		Primary:   r.PrimaryWindow.window(),
		Secondary: r.SecondaryWindow.window(),
		Reached:   r.LimitReached || (r.Allowed != nil && !*r.Allowed),
	}
}

type directUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

func (w *directUsageWindow) window() *codexWindow {
	if w == nil {
		return nil
	}
	return &codexWindow{UsedPercent: w.UsedPercent, DurationMins: w.LimitWindowSeconds / 60, ResetsAt: w.ResetAt}
}
