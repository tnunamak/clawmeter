// Package oauth provides reusable OAuth token refresh utilities.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const timeout = 10 * time.Second

// RefreshAccessToken exchanges a refresh token for a new access token.
func RefreshAccessToken(ctx context.Context, endpoint, clientID, clientSecret, refreshToken string) (string, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {refreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token refresh returned %d", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("token refresh error: %s", result.Error)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}

	return result.AccessToken, nil
}
