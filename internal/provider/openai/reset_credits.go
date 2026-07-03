package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	resetCreditsPath    = "/backend-api/wham/rate-limit-reset-credits"
	resetCreditsTimeout = 5 * time.Second
)

var (
	resetCreditsURL        = "https://chatgpt.com" + resetCreditsPath
	resetCreditsHTTPClient = &http.Client{Timeout: resetCreditsTimeout}
)

func (p *Provider) attachResetCredits(ctx context.Context, data *provider.UsageData) {
	if data == nil || data.Error != "" || data.IsExpired {
		return
	}
	auth, err := readAuthFile(codexHome())
	if err != nil {
		return
	}
	resetCredits, err := fetchResetCredits(ctx, auth)
	if err != nil {
		return
	}
	if resetCredits != nil && resetCredits.DisplayCount(time.Now()) > 0 {
		data.ResetCredits = resetCredits
	}
}

func fetchResetCredits(ctx context.Context, auth *authFile) (*provider.UsageResetCredits, error) {
	accessToken, accountID, ok := resetCreditAuth(auth)
	if !ok {
		return nil, nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, resetCreditsTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, resetCreditsURL, nil)
	if err != nil {
		return nil, err
	}
	if strings.Contains(req.URL.Path, "/consume") {
		return nil, fmt.Errorf("refusing reset-credit consume URL")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	req.Header.Set("Originator", "Codex Desktop")
	req.Header.Set("OAI-Product-Sku", "CODEX")
	req.Header.Set("Accept", "application/json")

	resp, err := resetCreditsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reset credits unavailable: http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseResetCredits(body, time.Now())
}

func resetCreditAuth(auth *authFile) (accessToken, accountID string, ok bool) {
	if auth == nil || auth.Tokens == nil {
		return "", "", false
	}
	accessToken = strings.TrimSpace(auth.Tokens.AccessToken)
	accountID = strings.TrimSpace(auth.Tokens.AccountID)
	if accessToken == "" {
		return "", "", false
	}
	return accessToken, accountID, true
}

type resetCreditsResponse struct {
	AvailableCount int                `json:"available_count"`
	Credits        []resetCreditEntry `json:"credits"`
}

type resetCreditEntry struct {
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	ConsumedAt string `json:"consumed_at"`
}

func parseResetCredits(data []byte, now time.Time) (*provider.UsageResetCredits, error) {
	var resp resetCreditsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse reset credits: %w", err)
	}

	result := &provider.UsageResetCredits{
		AvailableCount: resp.AvailableCount,
		FetchedAt:      now,
	}
	if result.AvailableCount < 0 {
		result.AvailableCount = 0
	}

	for _, raw := range resp.Credits {
		credit := provider.UsageResetCredit{
			Status: strings.TrimSpace(raw.Status),
		}
		if createdAt, ok := parseResetCreditTime(raw.CreatedAt); ok {
			credit.CreatedAt = createdAt
		}
		if expiresAt, ok := parseResetCreditTime(raw.ExpiresAt); ok {
			credit.ExpiresAt = expiresAt
		}
		if consumedAt, ok := parseResetCreditTime(raw.ConsumedAt); ok {
			credit.ConsumedAt = consumedAt
		}
		if strings.ToLower(credit.Status) != "available" {
			continue
		}
		if !credit.ConsumedAt.IsZero() {
			continue
		}
		if !credit.ExpiresAt.IsZero() && !credit.ExpiresAt.After(now) {
			continue
		}
		result.Credits = append(result.Credits, credit)
	}

	sort.SliceStable(result.Credits, func(i, j int) bool {
		a, b := result.Credits[i], result.Credits[j]
		if a.ExpiresAt.IsZero() {
			return false
		}
		if b.ExpiresAt.IsZero() {
			return true
		}
		return a.ExpiresAt.Before(b.ExpiresAt)
	})

	if result.AvailableCount != len(result.Credits) {
		result.Warning = "available reset count differs from detailed credit list"
	}
	if result.DisplayCount(now) == 0 {
		return nil, nil
	}
	return result, nil
}

func parseResetCreditTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
