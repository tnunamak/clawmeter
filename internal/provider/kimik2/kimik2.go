// Package kimik2 implements the Provider interface for Kimi K2.
package kimik2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	creditsURL = "https://kimi-k2.ai/api/user/credits"
	timeout    = 10 * time.Second
)

type Provider struct {
	cfg                        config.ProviderConfig
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	httpClient                 *http.Client
	creditsURL                 string
	sourceID                   string
	sourceLabel                string
	sourceCredentialKind       string
	sourceCredentialRef        string
	enrolledSource             bool
}

func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg, httpClient: &http.Client{Timeout: timeout}, creditsURL: creditsURL}
}

// NewNativeSource preserves the legacy Kimi K2 credential resolution chain while
// attaching an explicit identity to the compatibility default source.
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
	p, err := (&sourceCapability{}).NewSource(cfg, source)
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
		{Kind: "env-name", Summary: "Bearer token from the selected environment variable", RefUsage: "KIMI_K2_API_KEY", RefRequired: true, RefCaseInsensitive: true},
	}
}

func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

func (sourceCapability) ValidateSource(source config.SourceConfig) error {
	kind := strings.TrimSpace(source.Credential.Kind)
	ref := strings.TrimSpace(source.Credential.Ref)
	switch kind {
	case "native":
		if strings.TrimSpace(source.ID) != "default" || ref != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "kimik2", source.ID)
		}
	case "env-name":
		if !validEnvName(ref) {
			return fmt.Errorf("provider %q source %q has invalid environment variable name", "kimik2", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "kimik2", source.ID, kind)
	}
	return nil
}

func (sourceCapability) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(source); err != nil {
		return nil, err
	}
	p := New(cfg)
	p.sourceID = strings.TrimSpace(source.ID)
	p.sourceLabel = strings.TrimSpace(source.Label)
	p.sourceCredentialKind = strings.TrimSpace(source.Credential.Kind)
	p.sourceCredentialRef = strings.TrimSpace(source.Credential.Ref)
	p.enrolledSource = true
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
	if p.sourceCredentialKind == "env-name" {
		key, _ := p.getAPIKey()
		return provider.CredentialSourceRevision("env-name\x00"+p.sourceCredentialRef, key)
	}
	return ""
}

func (p *Provider) Name() string         { return "kimik2" }
func (p *Provider) DisplayName() string  { return "Kimi K2" }
func (p *Provider) Description() string  { return "Kimi K2 (via KIMI_K2_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://kimi-k2.ai/my-credits" }
func (p *Provider) SafeForAutoPolling() bool {
	return false
}

func (p *Provider) IsConfigured() bool {
	_, err := p.getAPIKey()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	apiKey, err := p.getAPIKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.creditsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &provider.UsageData{
			Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
			FetchedAt: time.Now(),
			IsExpired: true,
			Error:     "unauthorized — check KIMI_K2_API_KEY",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	// Parse flexibly — the API shape may vary
	var raw map[string]interface{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	consumed, hasConsumed, remaining, hasRemaining := p.extractCredits(raw)

	// Also check x-credits-remaining header as fallback
	if !hasRemaining {
		if hdr := resp.Header.Get("x-credits-remaining"); hdr != "" {
			if n, err := fmt.Sscanf(hdr, "%f", &remaining); err == nil && n == 1 {
				hasRemaining = true
			}
		}
	}

	data := &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	total := consumed + remaining
	if hasConsumed && hasRemaining && total > 0 {
		usedPct := (consumed / total) * 100
		if usedPct < 0 {
			usedPct = 0
		}
		if usedPct > 100 {
			usedPct = 100
		}
		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        "credits",
			DisplayName: "Credits",
			Utilization: usedPct,
		})
	} else {
		data.Error = "no credit data in response"
	}

	return data, nil
}

func (p *Provider) getAPIKey() (string, error) {
	if p.sourceCredentialKind != "" && p.sourceCredentialKind != "native" {
		return p.getExplicitSourceAPIKey()
	}
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}
	envNames := []string{"KIMI_K2_API_KEY", "KIMI_API_KEY", "KIMI_KEY"}
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: envNames, AllowSessionEnvironmentFallback: true})
		for _, env := range envNames {
			if key := values[env]; key != "" {
				return key, nil
			}
		}
	} else {
		for _, env := range envNames {
			if key := os.Getenv(env); key != "" {
				return key, nil
			}
		}
	}
	return "", fmt.Errorf("no API key found")
}

func (p *Provider) getExplicitSourceAPIKey() (string, error) {
	if p.sourceCredentialKind != "env-name" {
		return "", fmt.Errorf("unsupported credential kind %q", p.sourceCredentialKind)
	}
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: []string{p.sourceCredentialRef}, AllowSessionEnvironmentFallback: true})
		if key := values[p.sourceCredentialRef]; key != "" {
			return key, nil
		}
	} else if key := os.Getenv(p.sourceCredentialRef); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("environment variable %q is empty", p.sourceCredentialRef)
}

// extractCredits searches for consumed/remaining values in a flexible JSON structure.
func (p *Provider) extractCredits(obj map[string]interface{}) (consumed float64, hasConsumed bool, remaining float64, hasRemaining bool) {
	// Try root level, then nested under "data", "data.credits", "data.usage", "result", "result.credits"
	sources := []map[string]interface{}{obj}
	for _, key := range []string{"data", "result", "usage", "credits"} {
		if sub, ok := obj[key].(map[string]interface{}); ok {
			sources = append(sources, sub)
			// One more level: data.credits, data.usage, etc.
			for _, k2 := range []string{"credits", "usage"} {
				if sub2, ok := sub[k2].(map[string]interface{}); ok {
					sources = append(sources, sub2)
				}
			}
		}
	}

	consumedKeys := []string{"total_credits_consumed", "totalCreditsConsumed", "total_credits_used",
		"totalCreditsUsed", "credits_consumed", "creditsConsumed", "consumedCredits",
		"usedCredits", "used", "total", "consumed"}
	remainingKeys := []string{"credits_remaining", "creditsRemaining", "remaining_credits",
		"remainingCredits", "available_credits", "availableCredits", "credits_left",
		"creditsLeft", "remaining", "left", "available", "balance"}

	for _, src := range sources {
		if !hasConsumed {
			consumed, hasConsumed = provider.FindFloatPresent(src, consumedKeys)
		}
		if !hasRemaining {
			remaining, hasRemaining = provider.FindFloatPresent(src, remainingKeys)
		}
	}
	return
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("kimik2")
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}

var (
	envNamePattern                           = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	_              provider.SourceCapability = (*Provider)(nil)
)

func validEnvName(name string) bool {
	return envNamePattern.MatchString(strings.TrimSpace(name))
}
