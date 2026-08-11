// Package gemini implements the Provider interface for Google Gemini.
package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	quotaURL        = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	codeAssistURL   = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	tokenEndpoint   = "https://oauth2.googleapis.com/token"
	timeout         = 10 * time.Second
	consumerTierErr = "Gemini CLI consumer tier is no longer supported; use Antigravity"
)

// Provider implements the provider.Provider interface for Google Gemini.
type Provider struct {
	cfg            config.ProviderConfig
	sourceID       string
	sourceLabel    string
	configDir      string
	explicitSource bool
	enrolledSource bool
}

// New creates a new Gemini provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func NewNativeSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	if strings.TrimSpace(source.Credential.Kind) == "" {
		source.Credential.Kind = "native"
	}
	p, _ := newSource(cfg, source)
	return p
}

func NewSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	p, _ := newSource(cfg, source)
	return p
}

func newSource(cfg config.ProviderConfig, source config.SourceConfig) (*Provider, error) {
	p, err := (sourceCapability{}).NewSource(cfg, source)
	if err != nil {
		return nil, err
	}
	return p.(*Provider), nil
}

type sourceCapability struct{}

func (*Provider) SourceKinds() []provider.SourceKind { return (sourceCapability{}).SourceKinds() }
func (*Provider) DefaultSource() (config.SourceConfig, bool) {
	return (sourceCapability{}).DefaultSource()
}
func (*Provider) ValidateSource(source config.SourceConfig) error {
	return (sourceCapability{}).ValidateSource(source)
}
func (*Provider) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	return (sourceCapability{}).NewSource(cfg, source)
}

func (sourceCapability) SourceKinds() []provider.SourceKind {
	return []provider.SourceKind{
		{Kind: "native", Summary: "Provider's legacy/default credential route"},
		{Kind: "config-dir", Summary: "Gemini credential directory containing oauth_creds.json and settings.json; absolute path required", RefUsage: "/absolute/path", RefRequired: true, RefIsPath: true},
	}
}

func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

func (sourceCapability) ValidateSource(source config.SourceConfig) error {
	kind := strings.TrimSpace(source.Credential.Kind)
	switch kind {
	case "native":
		if strings.TrimSpace(source.ID) != "default" || strings.TrimSpace(source.Credential.Ref) != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "gemini", source.ID)
		}
	case "config-dir":
		ref := strings.TrimSpace(source.Credential.Ref)
		if ref == "" {
			return fmt.Errorf("provider %q source %q has empty config directory", "gemini", source.ID)
		}
		if !filepath.IsAbs(ref) {
			return fmt.Errorf("provider %q source %q has relative config directory", "gemini", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "gemini", source.ID, kind)
	}
	return nil
}

func (sourceCapability) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(source); err != nil {
		return nil, err
	}
	p := &Provider{cfg: cfg, sourceID: strings.TrimSpace(source.ID), sourceLabel: strings.TrimSpace(source.Label), enrolledSource: true}
	if strings.TrimSpace(source.Credential.Kind) == "config-dir" {
		p.configDir = strings.TrimSpace(source.Credential.Ref)
		p.explicitSource = true
	}
	return p, nil
}

func (p *Provider) SourceID() string {
	if p.sourceID == "" {
		return "default"
	}
	return p.sourceID
}
func (p *Provider) SourceLabel() string    { return p.sourceLabel }
func (p *Provider) IsEnrolledSource() bool { return p.enrolledSource }
func (p *Provider) SourceRevision() string {
	if !p.explicitSource {
		return ""
	}
	return sourceRevision(filepath.Join(p.configDir, "oauth_creds.json"), filepath.Join(p.configDir, "settings.json"))
}

func (p *Provider) Name() string         { return "gemini" }
func (p *Provider) DisplayName() string  { return "Gemini" }
func (p *Provider) Description() string  { return "Google Gemini (via OAuth credentials)" }
func (p *Provider) DashboardURL() string { return "https://aistudio.google.com" }

func (p *Provider) IsConfigured() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if !p.explicitSource && p.cfg.OAuthToken != "" {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "OAuth token configured"}
	}

	_, installedErr := exec.LookPath("gemini")
	installed := installedErr == nil

	creds, err := p.readCredentials()
	if err != nil {
		if installed {
			return provider.SetupStatus{
				State:  provider.SetupNeedsAuth,
				Detail: "Gemini CLI installed, sign in needed",
			}
		}
		return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "no OAuth credentials"}
	}

	oauthEnabled, oauthErr := p.isOAuthEnabled()
	if oauthErr != nil {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "Gemini OAuth settings unavailable",
		}
	}
	if !oauthEnabled {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "Gemini is not using Google account OAuth",
		}
	}

	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "Gemini credentials are empty"}
	}

	if creds.isExpired() {
		if creds.RefreshToken == "" {
			return provider.SetupStatus{
				State:  provider.SetupNeedsAuth,
				Detail: "Gemini token expired; sign in again",
			}
		}
		if _, err := discoverOAuthCredentials(); err != nil {
			return provider.SetupStatus{
				State:  provider.SetupNeedsAuth,
				Detail: "Gemini token expired and cannot be refreshed",
			}
		}
	}

	return provider.SetupStatus{State: provider.SetupReady, Detail: "OAuth credentials found"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	token, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	status, err := p.loadCodeAssistStatus(ctx, token)
	if err != nil {
		return nil, err
	}
	if status.ConsumerTierDeprecated() {
		return &provider.UsageData{
			Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
			FetchedAt: time.Now(),
			Error:     consumerTierErr,
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", quotaURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if status.ProjectID != "" {
		body, err := json.Marshal(map[string]string{"project": status.ProjectID})
		if err != nil {
			return nil, fmt.Errorf("encode quota request: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &provider.UsageData{
			Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
			FetchedAt: time.Now(),
			IsExpired: true,
			Error:     "unauthorized — reauth in Gemini Code Assist",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if isConsumerTierDeprecationSignal(body) {
			return &provider.UsageData{
				Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
				FetchedAt: time.Now(),
				Error:     consumerTierErr,
			}, nil
		}
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp quotaResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.transformQuota(&apiResp), nil
}

type codeAssistStatus struct {
	AllowedTiers    []codeAssistTier `json:"allowedTiers"`
	IneligibleTiers []codeAssistTier `json:"ineligibleTiers"`
	ProjectID       string           `json:"cloudaicompanionProject"`
	PaidTier        *codeAssistTier  `json:"paidTier"`
	Deprecated      bool             `json:"-"`
}

type codeAssistTier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s codeAssistStatus) ConsumerTierDeprecated() bool {
	if s.Deprecated {
		return true
	}
	if s.PaidTier != nil && s.PaidTier.Name != "" {
		return false
	}
	if len(s.AllowedTiers) == 0 {
		return false
	}
	for _, tier := range s.AllowedTiers {
		if tier.ID == "standard-tier" || tier.ID == "legacy-tier" || tier.ID == "enterprise-tier" {
			return false
		}
	}
	return true
}

func (p *Provider) loadCodeAssistStatus(ctx context.Context, token string) (codeAssistStatus, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", codeAssistURL, bytes.NewReader([]byte(`{"metadata":{"ideType":"GEMINI_CLI","pluginType":"GEMINI"}}`)))
	if err != nil {
		return codeAssistStatus{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return codeAssistStatus{}, fmt.Errorf("Code Assist status request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return codeAssistStatus{}, fmt.Errorf("read Code Assist status: %w", err)
	}
	if isConsumerTierDeprecationSignal(body) {
		return codeAssistStatus{Deprecated: true}, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return codeAssistStatus{}, fmt.Errorf("Code Assist status API returned %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return codeAssistStatus{}, fmt.Errorf("Code Assist status API returned %d", resp.StatusCode)
	}

	var status codeAssistStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return codeAssistStatus{}, fmt.Errorf("decode Code Assist status: %w", err)
	}
	return status, nil
}

func isConsumerTierDeprecationSignal(body []byte) bool {
	normalized := strings.ToLower(string(body))
	return strings.Contains(normalized, "unsupported_client") ||
		strings.Contains(normalized, "ineligibletiererror") ||
		(strings.Contains(normalized, "no longer supported") && strings.Contains(normalized, "gemini code assist")) ||
		(strings.Contains(normalized, "migrate") && strings.Contains(normalized, "antigravity") && strings.Contains(normalized, "gemini"))
}

// getAccessToken returns a valid access token, refreshing if needed.
func (p *Provider) getAccessToken(ctx context.Context) (string, error) {
	// Check config OAuth token
	if !p.explicitSource && p.cfg.OAuthToken != "" {
		return p.cfg.OAuthToken, nil
	}

	creds, err := p.readCredentials()
	if err != nil {
		return "", err
	}

	// Check if settings require oauth-personal
	oauthEnabled, err := p.isOAuthEnabled()
	if err != nil {
		return "", fmt.Errorf("gemini oauth settings: %w", err)
	}
	if !oauthEnabled {
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
	path, err := p.credentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
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

func (p *Provider) credentialsPath() (string, error) {
	if p.explicitSource {
		return filepath.Join(p.configDir, "oauth_creds.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".gemini", "oauth_creds.json"), nil
}

type geminiSettings struct {
	Security struct {
		Auth struct {
			SelectedType string `json:"selectedType"`
		} `json:"auth"`
	} `json:"security"`
}

func (p *Provider) isOAuthEnabled() (bool, error) {
	path, explicit, err := p.settingsPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if explicit {
			return false, fmt.Errorf("read settings: %w", err)
		}
		return true, nil // legacy fallback: assume enabled if no settings file
	}
	var settings geminiSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		if explicit {
			return false, fmt.Errorf("parse settings: %w", err)
		}
		return true, nil
	}
	if settings.Security.Auth.SelectedType != "" {
		return settings.Security.Auth.SelectedType == "oauth-personal", nil
	}
	return true, nil
}

func (p *Provider) settingsPath() (string, bool, error) {
	if p.explicitSource {
		return filepath.Join(p.configDir, "settings.json"), true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".gemini", "settings.json"), false, nil
}

// API response types

type quotaResponse struct {
	Buckets []quotaBucket `json:"buckets"`
}

type quotaBucket struct {
	ModelID           string   `json:"modelId"`
	RemainingFraction *float64 `json:"remainingFraction"` // 0.0-1.0
	ResetTime         string   `json:"resetTime"`         // ISO 8601 timestamp
}

func (p *Provider) transformQuota(resp *quotaResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	type tierInfo struct {
		worst   float64
		resetAt time.Time
		found   bool
	}
	pro := tierInfo{worst: 1.0}
	flash := tierInfo{worst: 1.0}

	for _, b := range resp.Buckets {
		if b.RemainingFraction == nil || *b.RemainingFraction < 0 || *b.RemainingFraction > 1 {
			continue
		}
		tier := &flash
		if isProModel(b.ModelID) {
			tier = &pro
		}
		tier.found = true
		if *b.RemainingFraction < tier.worst {
			tier.worst = *b.RemainingFraction
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
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        t.name,
			DisplayName: t.disp,
			Utilization: (1 - t.info.worst) * 100,
			ResetsAt:    t.info.resetAt,
		})
	}
	if len(data.Windows) == 0 {
		data.Error = "no complete quota data"
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
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}

func sourceRevision(paths ...string) string {
	h := sha256.New()
	for _, path := range paths {
		canonical, err := filepath.Abs(path)
		if err != nil {
			canonical = path
		}
		if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
			canonical = resolved
		}
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(h, "%s\x00unavailable\x00", canonical)
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%o\x00", canonical, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm())
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

var _ provider.SourceCapability = (*Provider)(nil)
