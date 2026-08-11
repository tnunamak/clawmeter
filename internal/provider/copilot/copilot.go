// Package copilot implements the Provider interface for GitHub Copilot.
package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	apiURL  = "https://api.github.com/copilot_internal/user"
	timeout = 10 * time.Second
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Provider implements the provider.Provider interface for GitHub Copilot.
type Provider struct {
	cfg                        config.ProviderConfig
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	source                     config.SourceConfig
	enrolled                   bool
}

func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
}

// New creates a new Copilot provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string         { return "copilot" }
func (p *Provider) DisplayName() string  { return "Copilot" }
func (p *Provider) Description() string  { return "GitHub Copilot (via GitHub token)" }
func (p *Provider) DashboardURL() string { return "https://github.com/settings/copilot" }

func (p *Provider) SourceKinds() []provider.SourceKind {
	return []provider.SourceKind{
		{Kind: "native", Summary: "default Copilot credential discovery"},
		{Kind: "env-name", Summary: "session environment variable name", RefUsage: "COPILOT_WORK_TOKEN", RefRequired: true, RefCaseInsensitive: true},
		{Kind: "credential-file", Summary: "absolute path to Copilot hosts.json", RefUsage: "/home/me/.config/github-copilot/hosts.json", RefRequired: true, RefIsPath: true},
	}
}

func (p *Provider) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

func (p *Provider) ValidateSource(source config.SourceConfig) error {
	switch source.Credential.Kind {
	case "native":
		if source.ID != "default" || source.Credential.Ref != "" {
			return fmt.Errorf("copilot source %q native credential must not have a ref", source.ID)
		}
	case "env-name":
		if !envNamePattern.MatchString(source.Credential.Ref) {
			return fmt.Errorf("copilot source %q env-name ref must be an environment variable name", source.ID)
		}
	case "credential-file":
		if !filepath.IsAbs(source.Credential.Ref) {
			return fmt.Errorf("copilot source %q credential-file ref must be an absolute path", source.ID)
		}
		if filepath.Base(source.Credential.Ref) != "hosts.json" {
			return fmt.Errorf("copilot source %q credential-file ref must reference exact hosts.json file", source.ID)
		}
	default:
		return fmt.Errorf("unsupported copilot credential kind %q", source.Credential.Kind)
	}
	return nil
}

func (p *Provider) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := p.ValidateSource(source); err != nil {
		return nil, err
	}
	return &Provider{cfg: cfg, source: source, enrolled: true}, nil
}

func (p *Provider) SourceID() string {
	if p.source.ID == "" {
		return "default"
	}
	return p.source.ID
}

func (p *Provider) SourceLabel() string {
	return p.source.Label
}

func (p *Provider) IsEnrolledSource() bool {
	return p.enrolled
}

func (p *Provider) SourceRevision() string {
	if !p.enrolled || p.source.Credential.Kind == "native" {
		return ""
	}
	material := p.source.Credential.Kind + "\x00" + p.source.Credential.Ref
	switch p.source.Credential.Kind {
	case "env-name":
		token, _ := p.getToken()
		return provider.CredentialSourceRevision(material, token)
	case "credential-file":
		info, err := os.Stat(p.source.Credential.Ref)
		if err != nil {
			material += "\x00unavailable"
		} else {
			material += fmt.Sprintf("\x00%d\x00%d\x00%o", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm())
		}
		return fmt.Sprintf("%x", sha256.Sum256([]byte(material)))
	default:
		return "native"
	}
}

func (p *Provider) IsConfigured() bool {
	_, err := p.getToken()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	token, err := p.getToken()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	return p.fetchUsage(ctx, &http.Client{Timeout: timeout}, apiURL, token)
}

func (p *Provider) fetchUsage(ctx context.Context, client *http.Client, endpoint, token string) (*provider.UsageData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Editor-Version", "vscode/1.96.2")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	req.Header.Set("X-Github-Api-Version", "2025-04-01")

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
			Error:     "no active subscription — enable at github.com/settings/copilot",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp userResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.transformUsage(&apiResp), nil
}

func (p *Provider) getToken() (string, error) {
	if p.enrolled {
		return p.getSourceToken()
	}

	// 1. Config API key
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}

	// 2. Copilot-specific env var (not GITHUB_TOKEN — a generic GitHub
	// token doesn't grant Copilot API access and causes false positives)
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
			EnvNames: []string{"COPILOT_API_TOKEN"}, AllowSessionEnvironmentFallback: true,
		})
		if token := values["COPILOT_API_TOKEN"]; token != "" {
			return token, nil
		}
	} else if token := os.Getenv("COPILOT_API_TOKEN"); token != "" {
		return token, nil
	}

	// 3. GitHub Copilot hosts.json (VS Code extension credential store)
	for _, path := range copilotHostsPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return tokenFromHostsJSON(data)
	}

	return "", fmt.Errorf("no copilot credentials found")
}

func (p *Provider) getSourceToken() (string, error) {
	switch p.source.Credential.Kind {
	case "native":
		cfg := p.cfg
		cfg.Sources = nil
		native := New(cfg)
		native.sessionEnvironmentResolver = p.sessionEnvironmentResolver
		return native.getToken()
	case "env-name":
		if p.sessionEnvironmentResolver != nil {
			values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
				EnvNames: []string{p.source.Credential.Ref}, AllowSessionEnvironmentFallback: true,
			})
			if token := values[p.source.Credential.Ref]; token != "" {
				return token, nil
			}
			return "", fmt.Errorf("no copilot credentials found for env %q", p.source.Credential.Ref)
		}
		if token := os.Getenv(p.source.Credential.Ref); token != "" {
			return token, nil
		}
		return "", fmt.Errorf("no copilot credentials found for env %q", p.source.Credential.Ref)
	case "credential-file":
		data, err := os.ReadFile(p.source.Credential.Ref)
		if err != nil {
			return "", fmt.Errorf("read copilot credential file: %w", err)
		}
		return tokenFromHostsJSON(data)
	default:
		return "", fmt.Errorf("unsupported copilot credential kind %q", p.source.Credential.Kind)
	}
}

// copilotHostsPaths returns platform-specific paths for the Copilot hosts.json file.
func copilotHostsPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	if runtime.GOOS == "windows" {
		var paths []string
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "github-copilot", "hosts.json"))
		}
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "github-copilot", "hosts.json"))
		}
		return paths
	}

	return []string{filepath.Join(home, ".config", "github-copilot", "hosts.json")}
}

func tokenFromHostsJSON(data []byte) (string, error) {

	var hosts map[string]struct {
		OAuthToken string `json:"oauth_token"`
	}
	if err := json.Unmarshal(data, &hosts); err != nil {
		return "", fmt.Errorf("parse hosts.json: %w", err)
	}

	if h, ok := hosts["github.com"]; ok && h.OAuthToken != "" {
		return h.OAuthToken, nil
	}

	return "", fmt.Errorf("no github.com token in hosts.json")
}

// API response types

type userResponse struct {
	QuotaSnapshots  map[string]quotaSnapshot `json:"quotaSnapshots"`
	QuotaSnapshots2 map[string]quotaSnapshot `json:"quota_snapshots"`
	QuotaResetDate  string                   `json:"quotaResetDate"`
	QuotaResetDate2 string                   `json:"quota_reset_date"`
}

type quotaSnapshot struct {
	PercentRemaining *float64 `json:"percentRemaining"`
}

func (p *Provider) transformUsage(resp *userResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}
	resetAt, resetKnown := parseResetDate(resp.QuotaResetDate)
	if !resetKnown {
		resetAt, resetKnown = parseResetDate(resp.QuotaResetDate2)
	}
	if !resetKnown && (resp.QuotaResetDate != "" || resp.QuotaResetDate2 != "") {
		data.Warning = "Copilot returned an invalid quota reset date; reset is unknown"
	}
	snapshots := resp.QuotaSnapshots
	if len(snapshots) == 0 {
		snapshots = resp.QuotaSnapshots2
	}

	if snap, ok := snapshots["premiumInteractions"]; ok && validPercentRemaining(snap.PercentRemaining) {
		usedPct := clamp(100-*snap.PercentRemaining, 0, 100)
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "premium",
			DisplayName: "Premium",
			Utilization: usedPct,
			ResetsAt:    resetAt,
		})
	}

	if snap, ok := snapshots["chat"]; ok && validPercentRemaining(snap.PercentRemaining) {
		usedPct := clamp(100-*snap.PercentRemaining, 0, 100)
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "chat",
			DisplayName: "Chat",
			Utilization: usedPct,
			ResetsAt:    resetAt,
		})
	}

	if len(data.Windows) == 0 {
		data.Error = "no quota data in response"
	}
	if !resetKnown && len(data.Windows) > 0 && data.Warning == "" {
		data.Warning = "Copilot quota reset date is not available; reset is unknown"
	}

	return data
}

func validPercentRemaining(percent *float64) bool {
	return percent != nil && *percent >= 0 && *percent <= 100
}

func parseResetDate(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Register registers the Copilot provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("copilot")
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}
