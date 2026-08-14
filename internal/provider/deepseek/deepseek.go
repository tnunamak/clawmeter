// Package deepseek implements DeepSeek's read-only account balance endpoint.
package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	balanceURL = "https://api.deepseek.com/user/balance"
	maxBody    = 1 << 20
	timeout    = 10 * time.Second
)

type Provider struct {
	cfg                        config.ProviderConfig
	client                     *http.Client
	balanceURL                 string
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	sourceID, sourceLabel      string
	sourceCredential           string
	enrolledSource             bool
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: timeout}, balanceURL: balanceURL}
}

func (p *Provider) SetSessionEnvironmentResolver(r provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = r
}
func (p *Provider) Name() string             { return "deepseek" }
func (p *Provider) DisplayName() string      { return "DeepSeek" }
func (p *Provider) Description() string      { return "DeepSeek API balance" }
func (p *Provider) DashboardURL() string     { return "https://platform.deepseek.com/" }
func (p *Provider) SafeForAutoPolling() bool { return true }
func (p *Provider) IsConfigured() bool       { return p.apiKey() != "" }

type sourceCapability struct{}

func (*Provider) SourceKinds() []provider.SourceKind { return (sourceCapability{}).SourceKinds() }
func (*Provider) DefaultSource() (config.SourceConfig, bool) {
	return (sourceCapability{}).DefaultSource()
}
func (*Provider) ValidateSource(s config.SourceConfig) error {
	return (sourceCapability{}).ValidateSource(s)
}
func (*Provider) NewSource(cfg config.ProviderConfig, s config.SourceConfig) (provider.Provider, error) {
	return (sourceCapability{}).NewSource(cfg, s)
}
func (sourceCapability) SourceKinds() []provider.SourceKind {
	return []provider.SourceKind{
		{Kind: "native", Summary: "DeepSeek API key from config or DEEPSEEK_API_KEY"},
		{Kind: "env-name", Summary: "DeepSeek API key environment variable name", RefUsage: "DEEPSEEK_API_KEY", RefRequired: true, RefCaseInsensitive: true},
	}
}
func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (sourceCapability) ValidateSource(s config.SourceConfig) error {
	kind, ref := strings.TrimSpace(s.Credential.Kind), strings.TrimSpace(s.Credential.Ref)
	switch kind {
	case "native":
		if strings.TrimSpace(s.ID) != "default" || ref != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "deepseek", s.ID)
		}
	case "env-name":
		if !envNamePattern.MatchString(ref) {
			return fmt.Errorf("provider %q source %q has invalid environment variable name", "deepseek", s.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "deepseek", s.ID, kind)
	}
	return nil
}
func (sourceCapability) NewSource(cfg config.ProviderConfig, s config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(s); err != nil {
		return nil, err
	}
	p := New(cfg)
	p.sourceID, p.sourceLabel, p.sourceCredential = strings.TrimSpace(s.ID), strings.TrimSpace(s.Label), strings.TrimSpace(s.Credential.Ref)
	p.enrolledSource = true
	if s.Credential.Kind == "native" {
		p.sourceCredential = ""
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
	if p.sourceCredential == "" {
		return ""
	}
	return provider.CredentialSourceRevision("env-name\x00"+p.sourceCredential, p.apiKey())
}

func (p *Provider) apiKey() string {
	if p.sourceCredential != "" {
		return p.envValue([]string{p.sourceCredential})
	}
	if key := strings.TrimSpace(p.cfg.APIKey); key != "" {
		return key
	}
	return p.envValue([]string{"DEEPSEEK_API_KEY"})
}
func (p *Provider) envValue(names []string) string {
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: names, AllowSessionEnvironmentFallback: true})
		for _, name := range names {
			if key := strings.TrimSpace(values[name]); key != "" {
				return key
			}
		}
		return ""
	}
	for _, name := range names {
		if key := strings.TrimSpace(os.Getenv(name)); key != "" {
			return key
		}
	}
	return ""
}

type response struct {
	IsAvailable  bool          `json:"is_available"`
	BalanceInfos []balanceInfo `json:"balance_infos"`
}
type balanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	key := p.apiKey()
	if key == "" {
		return nil, fmt.Errorf("credentials: no API key found")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.balanceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data := &provider.UsageData{Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(), FetchedAt: time.Now()}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		data.IsExpired, data.InvalidatesPriorUsage, data.Error = true, true, "DeepSeek API key expired or unauthorized"
		return data, nil
	case http.StatusPaymentRequired:
		data.Warning = "DeepSeek balance is zero or insufficient"
		return data, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("DeepSeek balance returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBody)
	}
	var payload response
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if payload.BalanceInfos == nil {
		return nil, fmt.Errorf("decode response: missing balance_infos")
	}
	seenCurrencies := make(map[string]bool, len(payload.BalanceInfos))
	for _, balance := range payload.BalanceInfos {
		currency := strings.TrimSpace(balance.Currency)
		if currency == "" {
			return nil, fmt.Errorf("decode response: empty currency")
		}
		normalized := strings.ToLower(currency)
		if seenCurrencies[normalized] {
			return nil, fmt.Errorf("decode response: duplicate currency %q", currency)
		}
		seenCurrencies[normalized] = true
		total, err := strconv.ParseFloat(strings.TrimSpace(balance.TotalBalance), 64)
		if err != nil || math.IsNaN(total) || math.IsInf(total, 0) || total < 0 {
			return nil, fmt.Errorf("decode response: invalid total balance for %q", currency)
		}
		data.Balances = append(data.Balances, provider.UsageBalance{Name: normalized, DisplayName: strings.ToUpper(currency) + " balance", Remaining: total})
	}
	sort.SliceStable(data.Balances, func(i, j int) bool { return data.Balances[i].Name < data.Balances[j].Name })
	if !payload.IsAvailable {
		data.Warning = "DeepSeek balance endpoint reports the account is unavailable"
	}
	return data, nil
}

var _ provider.SourceCapability = (*Provider)(nil)
