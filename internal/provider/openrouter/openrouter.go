// Package openrouter implements the Provider interface for OpenRouter.
package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	creditsURL = "https://openrouter.ai/api/v1/credits"
	keyURL     = "https://openrouter.ai/api/v1/key"
	timeout    = 10 * time.Second
	maxBody    = 1 << 20
)

type Provider struct {
	cfg                        config.ProviderConfig
	client                     *http.Client
	creditsURL, keyURL         string
	managementKey              string
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	credentialEnvOnce          sync.Once
	credentialEnv              map[string]string
	sourceID                   string
	sourceLabel                string
	sourceCredential           config.CredentialRef
	explicitSource             bool
	enrolledSource             bool
}

func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
}

func (p *Provider) credentialEnvValues() map[string]string {
	if p.sessionEnvironmentResolver == nil {
		return nil
	}
	if p.explicitSource {
		name := strings.TrimSpace(p.sourceCredential.Ref)
		if name == "" {
			return nil
		}
		return p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
			EnvNames: []string{name}, AllowSessionEnvironmentFallback: true,
		})
	}
	p.credentialEnvOnce.Do(func() {
		p.credentialEnv = p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
			EnvNames: []string{"OPENROUTER_API_KEY", "OPENROUTER_MANAGEMENT_KEY"}, AllowSessionEnvironmentFallback: true,
		})
	})
	return p.credentialEnv
}

type apiError int

func (e apiError) Error() string { return fmt.Sprintf("API returned %d", int(e)) }

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: timeout}, creditsURL: creditsURL, keyURL: keyURL}
}
func (p *Provider) Name() string             { return "openrouter" }
func (p *Provider) DisplayName() string      { return "OpenRouter" }
func (p *Provider) Description() string      { return "OpenRouter (via OPENROUTER_API_KEY)" }
func (p *Provider) DashboardURL() string     { return "https://openrouter.ai/credits" }
func (p *Provider) SafeForAutoPolling() bool { return false }
func (p *Provider) IsConfigured() bool       { return p.standardKey() != "" || p.managementAPIKey() != "" }

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
		{Kind: "api-key-env-name", Summary: "OpenRouter API key environment variable name", RefUsage: "OPENROUTER_API_KEY", RefRequired: true, RefCaseInsensitive: true},
		{Kind: "management-key-env-name", Summary: "OpenRouter management key environment variable name", RefUsage: "OPENROUTER_MANAGEMENT_KEY", RefRequired: true, RefCaseInsensitive: true},
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
			return fmt.Errorf("provider %q source %q cannot use native credentials", "openrouter", source.ID)
		}
	case "api-key-env-name", "management-key-env-name":
		if !openrouterEnvNamePattern.MatchString(ref) {
			return fmt.Errorf("provider %q source %q has empty environment variable name", "openrouter", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "openrouter", source.ID, kind)
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
	p.sourceCredential = config.CredentialRef{Kind: strings.TrimSpace(source.Credential.Kind), Ref: strings.TrimSpace(source.Credential.Ref)}
	p.enrolledSource = true
	p.explicitSource = p.sourceCredential.Kind != "native"
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
	credential := p.standardKey()
	if p.sourceCredential.Kind == "management-key-env-name" {
		credential = p.managementAPIKey()
	}
	return provider.CredentialSourceRevision(p.sourceCredential.Kind+"\x00"+p.sourceCredential.Ref, credential)
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	standardKey, managementKey := p.standardKey(), p.managementAPIKey()
	if standardKey == "" && managementKey == "" {
		return nil, fmt.Errorf("credentials: no API key found")
	}
	data := &provider.UsageData{Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(), FetchedAt: time.Now()}
	if standardKey != "" {
		keyData, keyErr := p.fetchKey(ctx, standardKey)
		if keyErr != nil {
			return p.authOrError(keyErr, "OPENROUTER_API_KEY")
		}
		applyKey(data, keyData)
	}
	if managementKey != "" {
		wallet, walletErr := p.fetchCredits(ctx, managementKey)
		if walletErr != nil {
			if len(data.Windows) == 0 {
				return p.authOrError(walletErr, "OPENROUTER_MANAGEMENT_KEY")
			}
			data.Warning = "wallet credits unavailable: " + walletErr.Error()
		} else {
			data.Balances = wallet.Balances
		}
	}
	if len(data.Windows) == 0 && len(data.Balances) == 0 && data.Warning == "" {
		data.Warning = "no usable OpenRouter limits returned"
	}
	return data, nil
}

func (p *Provider) authOrError(err error, name string) (*provider.UsageData, error) {
	if status, ok := err.(apiError); ok && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		return &provider.UsageData{Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(), FetchedAt: time.Now(), IsExpired: true, Error: "unauthorized — check " + name}, nil
	}
	return nil, err
}
func (p *Provider) standardKey() string {
	if p.explicitSource && p.sourceCredential.Kind != "api-key-env-name" {
		return ""
	}
	if !p.explicitSource && strings.TrimSpace(p.cfg.APIKey) != "" {
		return strings.TrimSpace(p.cfg.APIKey)
	}
	if p.sessionEnvironmentResolver != nil {
		values := p.credentialEnvValues()
		name := "OPENROUTER_API_KEY"
		if p.explicitSource {
			name = strings.TrimSpace(p.sourceCredential.Ref)
		}
		if key := strings.TrimSpace(values[name]); key != "" {
			return key
		}
	} else {
		name := "OPENROUTER_API_KEY"
		if p.explicitSource {
			name = strings.TrimSpace(p.sourceCredential.Ref)
		}
		if key := strings.TrimSpace(os.Getenv(name)); key != "" {
			return key
		}
	}
	return ""
}
func (p *Provider) managementAPIKey() string {
	if p.explicitSource && p.sourceCredential.Kind != "management-key-env-name" {
		return ""
	}
	if !p.explicitSource && strings.TrimSpace(p.managementKey) != "" {
		return strings.TrimSpace(p.managementKey)
	}
	if p.sessionEnvironmentResolver != nil {
		values := p.credentialEnvValues()
		name := "OPENROUTER_MANAGEMENT_KEY"
		if p.explicitSource {
			name = strings.TrimSpace(p.sourceCredential.Ref)
		}
		return strings.TrimSpace(values[name])
	}
	name := "OPENROUTER_MANAGEMENT_KEY"
	if p.explicitSource {
		name = strings.TrimSpace(p.sourceCredential.Ref)
	}
	return strings.TrimSpace(os.Getenv(name))
}

var openrouterEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type creditsResponse struct {
	Data creditsData `json:"data"`
}
type creditsData struct {
	TotalCredits *float64 `json:"total_credits"`
	TotalUsage   *float64 `json:"total_usage"`
}
type keyResponse struct {
	Data keyData `json:"data"`
}
type keyData struct {
	Limit          *float64 `json:"limit"`
	LimitRemaining *float64 `json:"limit_remaining"`
	Usage          *float64 `json:"usage"`
	LimitReset     string   `json:"limit_reset"`
}

func (p *Provider) request(ctx context.Context, url, key string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
func (p *Provider) fetchCredits(ctx context.Context, key string) (*provider.UsageData, error) {
	var resp creditsResponse
	if err := p.request(ctx, p.creditsURL, key, &resp); err != nil {
		return nil, err
	}
	if resp.Data.TotalCredits == nil || resp.Data.TotalUsage == nil {
		return nil, fmt.Errorf("decode response: missing credits fields")
	}
	total, used := *resp.Data.TotalCredits, *resp.Data.TotalUsage
	remaining := total - used
	if remaining < 0 {
		remaining = 0
	}
	return &provider.UsageData{Provider: p.Name(), FetchedAt: time.Now(), Balances: []provider.UsageBalance{{Name: "credits", DisplayName: "Credits", Total: total, Used: used, Remaining: remaining}}}, nil
}
func (p *Provider) fetchKey(ctx context.Context, key string) (*keyData, error) {
	var resp keyResponse
	if err := p.request(ctx, p.keyURL, key, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}
func applyKey(data *provider.UsageData, k *keyData) {
	if k.Limit == nil || k.LimitRemaining == nil || k.Usage == nil || *k.Limit < 0 {
		return
	}
	limit := *k.Limit
	remaining := *k.LimitRemaining
	if limit <= 0 || remaining < 0 {
		return
	}
	used := limit - remaining
	pct := used / limit * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	data.Windows = append(data.Windows, provider.UsageWindow{Name: "key", DisplayName: "API key", Utilization: pct, Limit: int(limit), Used: int(used), ResetPolicy: k.LimitReset})
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("openrouter")
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}

var _ provider.SourceCapability = (*Provider)(nil)
