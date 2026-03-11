// Package gemini implements the Provider interface for Google Gemini.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/oauth"
)

const (
	quotaURL      = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	timeout       = 10 * time.Second
)

// Provider implements the provider.Provider interface for Google Gemini.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new Gemini provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func (p *Provider) Name() string        { return "gemini" }
func (p *Provider) DisplayName() string  { return "Gemini" }
func (p *Provider) Description() string  { return "Google Gemini (via OAuth credentials)" }
func (p *Provider) DashboardURL() string { return "https://aistudio.google.com" }

func (p *Provider) IsConfigured() bool {
	if p.cfg.OAuthToken != "" {
		return true
	}
	_, err := p.readCredentials()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	token, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", quotaURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

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
			Error:     "unauthorized — reauth in Gemini Code Assist",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp quotaResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.transformQuota(&apiResp), nil
}

// getAccessToken returns a valid access token, refreshing if needed.
func (p *Provider) getAccessToken(ctx context.Context) (string, error) {
	// Check config OAuth token
	if p.cfg.OAuthToken != "" {
		return p.cfg.OAuthToken, nil
	}

	creds, err := p.readCredentials()
	if err != nil {
		return "", err
	}

	// Check if settings require oauth-personal
	if !p.isOAuthEnabled() {
		return "", fmt.Errorf("gemini oauth not enabled in settings")
	}

	// Refresh if expired
	if creds.isExpired() {
		if creds.RefreshToken == "" {
			return "", fmt.Errorf("token expired and no refresh token")
		}
		oauthCreds, err := discoverOAuthCredentials()
		if err != nil {
			return "", fmt.Errorf("cannot refresh token: %w", err)
		}
		token, err := oauth.RefreshAccessToken(ctx, tokenEndpoint, oauthCreds.clientID, oauthCreds.clientSecret, creds.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("token refresh: %w", err)
		}
		return token, nil
	}

	return creds.AccessToken, nil
}

type oauthCredentials struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiryDate   float64 `json:"expiry_date"` // milliseconds since epoch
}

func (c *oauthCredentials) isExpired() bool {
	if c.ExpiryDate == 0 {
		return false
	}
	return float64(time.Now().UnixMilli()) >= c.ExpiryDate
}

func (p *Provider) readCredentials() (*oauthCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".gemini", "oauth_creds.json"))
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	var creds oauthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return nil, fmt.Errorf("no tokens in credentials file")
	}

	return &creds, nil
}

type geminiSettings struct {
	Security struct {
		Auth struct {
			SelectedType string `json:"selectedType"`
		} `json:"auth"`
	} `json:"security"`
}

func (p *Provider) isOAuthEnabled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true // assume enabled if can't check
	}

	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		return true // assume enabled if no settings file
	}

	var settings geminiSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return true
	}

	// If settings specify a type, it must be oauth-personal
	if settings.Security.Auth.SelectedType != "" {
		return settings.Security.Auth.SelectedType == "oauth-personal"
	}
	return true
}

// API response types

type quotaResponse struct {
	Buckets []quotaBucket `json:"buckets"`
}

type quotaBucket struct {
	ModelID           string  `json:"modelId"`
	RemainingFraction float64 `json:"remainingFraction"` // 0.0-1.0
	ResetTime         string  `json:"resetTime"`         // ISO 8601 timestamp
}

func (p *Provider) transformQuota(resp *quotaResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	type tierInfo struct {
		worst    float64
		resetAt  time.Time
		found    bool
	}
	pro := tierInfo{worst: 1.0}
	flash := tierInfo{worst: 1.0}

	for _, b := range resp.Buckets {
		tier := &flash
		if isProModel(b.ModelID) {
			tier = &pro
		}
		tier.found = true
		if b.RemainingFraction < tier.worst {
			tier.worst = b.RemainingFraction
			if t, err := time.Parse(time.RFC3339, b.ResetTime); err == nil {
				tier.resetAt = t
			} else if t, err := time.Parse(time.RFC3339Nano, b.ResetTime); err == nil {
				tier.resetAt = t
			}
		}
	}

	for _, t := range []struct {
		info tierInfo
		name string
		disp string
	}{
		{pro, "24h Pro", "Pro (24h)"},
		{flash, "24h Flash", "Flash (24h)"},
	} {
		if !t.info.found {
			continue
		}
		resetAt := t.info.resetAt
		if resetAt.IsZero() {
			resetAt = time.Now().Add(24 * time.Hour)
		}
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        t.name,
			DisplayName: t.disp,
			Utilization: (1 - t.info.worst) * 100,
			ResetsAt:    resetAt,
		})
	}

	return data
}

func isProModel(modelID string) bool {
	return strings.Contains(strings.ToLower(modelID), "pro")
}

// appOAuthCreds holds the application-level OAuth client ID and secret
// discovered from the installed gemini-cli.
type appOAuthCreds struct {
	clientID     string
	clientSecret string
}

var (
	oauthClientIDPattern     = regexp.MustCompile(`OAUTH_CLIENT_ID\s*=\s*['"]([^'"]+)['"]`)
	oauthClientSecretPattern = regexp.MustCompile(`OAUTH_CLIENT_SECRET\s*=\s*['"]([^'"]+)['"]`)
)

// discoverOAuthCredentials reads the OAuth client ID and secret from the
// installed gemini-cli's source, following CodexBar's approach. These are
// application-level credentials shipped with every gemini-cli install.
func discoverOAuthCredentials() (*appOAuthCreds, error) {
	geminiPath, err := exec.LookPath("gemini")
	if err != nil {
		return nil, fmt.Errorf("gemini-cli not found in PATH")
	}

	// Resolve symlinks to find the real installation
	realPath, err := filepath.EvalSymlinks(geminiPath)
	if err != nil {
		realPath = geminiPath
	}

	binDir := filepath.Dir(realPath)
	baseDir := filepath.Dir(binDir)

	oauthFile := "node_modules/@google/gemini-cli/node_modules/@google/gemini-cli-core/dist/src/code_assist/oauth2.js"
	candidates := []string{
		filepath.Join(baseDir, "lib", oauthFile),
		filepath.Join(baseDir, "libexec", "lib", oauthFile),
		filepath.Join(baseDir, "share", "gemini-cli", "node_modules/@google/gemini-cli-core/dist/src/code_assist/oauth2.js"),
		filepath.Join(baseDir, "node_modules/@google/gemini-cli-core/dist/src/code_assist/oauth2.js"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)

		idMatch := oauthClientIDPattern.FindStringSubmatch(content)
		secretMatch := oauthClientSecretPattern.FindStringSubmatch(content)
		if len(idMatch) < 2 || len(secretMatch) < 2 {
			continue
		}

		return &appOAuthCreds{
			clientID:     idMatch[1],
			clientSecret: secretMatch[1],
		}, nil
	}

	return nil, fmt.Errorf("could not find OAuth credentials in gemini-cli installation")
}

// Register registers the Gemini provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("gemini")
	return registry.Register(New(providerCfg))
}
