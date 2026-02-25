package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	usageURL   = "https://api.anthropic.com/api/oauth/usage"
	betaHeader = "oauth-2025-04-20"
	timeout    = 5 * time.Second
)

// ReadCredentials tries, in order:
//  1. CLAUDE_CODE_OAUTH_TOKEN env var (raw access token)
//  2. macOS Keychain (security find-generic-password)
//  3. ~/.claude/.credentials.json file (Linux)
func ReadCredentials() (*OAuthCredentials, error) {
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		return &OAuthCredentials{tokenOnly: token}, nil
	}

	if runtime.GOOS == "darwin" {
		if creds, err := readKeychain(); err == nil {
			return creds, nil
		}
	}

	return readCredentialsFile()
}

func readKeychain() (*OAuthCredentials, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain: %w", err)
	}
	data := strings.TrimSpace(string(out))
	if data == "" {
		return nil, fmt.Errorf("keychain: empty value")
	}
	var creds OAuthCredentials
	if err := json.Unmarshal([]byte(data), &creds); err != nil {
		// Might be a raw token string
		return &OAuthCredentials{tokenOnly: data}, nil
	}
	return &creds, nil
}

func readCredentialsFile() (*OAuthCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var creds OAuthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return &creds, nil
}

func (c *OAuthCredentials) AccessToken() string {
	if c.tokenOnly != "" {
		return c.tokenOnly
	}
	return c.ClaudeAiOauth.AccessToken
}

func (c *OAuthCredentials) IsExpired() bool {
	if c.tokenOnly != "" {
		return false // can't check expiry for raw tokens
	}
	return time.Now().UnixMilli() >= c.ClaudeAiOauth.ExpiresAt
}

func (c *OAuthCredentials) ExpiresIn() time.Duration {
	return time.Until(time.UnixMilli(c.ClaudeAiOauth.ExpiresAt))
}

func FetchUsage(accessToken string) (*UsageResponse, error) {
	req, err := http.NewRequest("GET", usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", betaHeader)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var usage UsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &usage, nil
}
