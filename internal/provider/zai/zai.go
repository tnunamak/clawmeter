// Package zai implements the Provider interface for z.ai (Zhipu AI / GLM).
package zai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	defaultBaseURL = "https://api.z.ai"
	quotaPath      = "/api/monitor/usage/quota/limit"
	timeout        = 10 * time.Second
	maxBodySize    = 1 << 20
)

var credentialEnvNames = []string{"Z_AI_API_KEY", "Z_AI_QUOTA_URL", "Z_AI_API_HOST", "Z_AI_REGION"}

type Provider struct {
	cfg                        config.ProviderConfig
	client                     *http.Client
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	sourceID                   string
	sourceLabel                string
	sourceCredential           config.CredentialRef
	explicitSource             bool
	enrolledSource             bool
}

var zaiEnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: timeout}}
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
		{Kind: "api-key-env-name", Summary: "z.ai global API-key environment variable", RefUsage: "ZAI_WORK_API_KEY", RefRequired: true, RefCaseInsensitive: true},
		{Kind: "cn-api-key-env-name", Summary: "Zhipu China API-key environment variable", RefUsage: "ZAI_CN_API_KEY", RefRequired: true, RefCaseInsensitive: true},
	}
}

func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

func (sourceCapability) ValidateSource(source config.SourceConfig) error {
	kind, ref := strings.TrimSpace(source.Credential.Kind), strings.TrimSpace(source.Credential.Ref)
	switch kind {
	case "native":
		if strings.TrimSpace(source.ID) != "default" || ref != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "zai", source.ID)
		}
	case "api-key-env-name", "cn-api-key-env-name":
		if !zaiEnvNamePattern.MatchString(ref) {
			return fmt.Errorf("provider %q source %q has invalid environment variable name", "zai", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "zai", source.ID, kind)
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
	p.explicitSource = p.sourceCredential.Kind != "native"
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
	if !p.explicitSource {
		return ""
	}
	key, _ := p.getAPIKey()
	return provider.CredentialSourceRevision(p.sourceCredential.Kind+"\x00"+p.sourceCredential.Ref, key)
}

func (p *Provider) withSource(data *provider.UsageData) *provider.UsageData {
	if data != nil {
		data.Provider, data.SourceID, data.SourceLabel = p.Name(), p.SourceID(), p.SourceLabel()
	}
	return data
}

func (p *Provider) Name() string         { return "zai" }
func (p *Provider) DisplayName() string  { return "z.ai" }
func (p *Provider) Description() string  { return "Zhipu AI / GLM (via Z_AI_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://z.ai/manage-apikey/subscription" }
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

	url := p.getQuotaURL()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+apiKey)
	req.Header.Set("accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return p.withSource(&provider.UsageData{
			Provider:  p.Name(),
			FetchedAt: time.Now(),
			IsExpired: true,
			Error:     "unauthorized — check Z_AI_API_KEY",
		}), nil
	}

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodySize))
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodySize)).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success || apiResp.Code != 200 {
		return nil, fmt.Errorf("API error code %d", apiResp.Code)
	}

	return p.withSource(p.transformLimits(&apiResp)), nil
}

func (p *Provider) getAPIKey() (string, error) {
	if p.explicitSource {
		name := p.sourceCredential.Ref
		key := strings.TrimSpace(os.Getenv(name))
		if p.sessionEnvironmentResolver != nil {
			values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: []string{name}, AllowSessionEnvironmentFallback: true})
			key = strings.TrimSpace(values[name])
		}
		if key == "" {
			return "", fmt.Errorf("selected API key is unavailable")
		}
		return strings.Trim(key, "\" "), nil
	}
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}
	if p.sessionEnvironmentResolver != nil {
		values := p.credentialEnvValues()
		if key := values["Z_AI_API_KEY"]; key != "" {
			return strings.Trim(key, "\" "), nil
		}
	} else if key := os.Getenv("Z_AI_API_KEY"); key != "" {
		return strings.Trim(key, "\" "), nil
	}
	return "", fmt.Errorf("no API key found")
}

func (p *Provider) getQuotaURL() string {
	if p.explicitSource {
		if p.sourceCredential.Kind == "cn-api-key-env-name" {
			return "https://open.bigmodel.cn" + quotaPath
		}
		return defaultBaseURL + quotaPath
	}
	values := map[string]string{}
	if p.sessionEnvironmentResolver != nil {
		values = p.credentialEnvValues()
	} else {
		values = map[string]string{
			"Z_AI_QUOTA_URL": os.Getenv("Z_AI_QUOTA_URL"),
			"Z_AI_API_HOST":  os.Getenv("Z_AI_API_HOST"),
			"Z_AI_REGION":    os.Getenv("Z_AI_REGION"),
		}
	}
	if raw := strings.TrimSpace(values["Z_AI_QUOTA_URL"]); raw != "" {
		if endpoint, ok := safeEndpoint(raw); ok {
			return endpoint
		}
		return ""
	}
	if raw := strings.TrimSpace(values["Z_AI_API_HOST"]); raw != "" {
		if endpoint, ok := safeEndpoint(raw); ok {
			return strings.TrimRight(endpoint, "/") + quotaPath
		}
		return ""
	}
	base := defaultBaseURL
	if strings.EqualFold(values["Z_AI_REGION"], "cn") {
		base = "https://open.bigmodel.cn"
	}
	return base + quotaPath
}

func (p *Provider) credentialEnvValues() map[string]string {
	return p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
		EnvNames: append([]string(nil), credentialEnvNames...), AllowSessionEnvironmentFallback: true,
	})
}

func safeEndpoint(raw string) (string, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), "\"'")
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return u.String(), true
}

// API response types

type apiResponse struct {
	Code    int     `json:"code"`
	Msg     string  `json:"msg"`
	Success bool    `json:"success"`
	Data    apiData `json:"data"`
}

type apiData struct {
	Limits   []apiLimit `json:"limits"`
	PlanName string     `json:"planName"`
}

type apiLimit struct {
	Type          string `json:"type"`         // "TOKENS_LIMIT" or "TIME_LIMIT"
	Unit          int    `json:"unit"`         // 0=unknown, 1=days, 3=hours, 5=minutes
	Number        int    `json:"number"`       // multiplier for unit
	Usage         *int64 `json:"usage"`        // total limit
	CurrentValue  *int64 `json:"currentValue"` // amount used
	Remaining     *int64 `json:"remaining"`
	Percentage    *int   `json:"percentage"`    // 0-100 fallback
	NextResetTime *int64 `json:"nextResetTime"` // milliseconds epoch; absent means unknown
}

func (p *Provider) transformLimits(resp *apiResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	tokenIndex := 0
	for _, limit := range resp.Data.Limits {
		name := "time"
		displayName := "Tokens"
		if limit.Type == "TIME_LIMIT" {
			displayName = "Time"
		} else if limit.Type == "TOKENS_LIMIT" {
			tokenIndex++
			name = tokenWindowName(limit, tokenIndex)
		} else {
			continue
		}

		// Compute utilization
		used, total := limitUsage(limit)
		var usedPct float64
		if total > 0 {
			usedPct = float64(used) / float64(total) * 100
		} else if limit.Percentage != nil {
			usedPct = float64(*limit.Percentage)
		} else {
			continue
		}
		if usedPct < 0 {
			usedPct = 0
		}
		if usedPct > 100 {
			usedPct = 100
		}

		// Parse reset time (milliseconds epoch)
		var resetsAt time.Time
		if limit.NextResetTime != nil && *limit.NextResetTime > 0 {
			resetsAt = time.UnixMilli(*limit.NextResetTime)
		}

		// Add window duration to display name
		windowDesc := unitToString(limit.Unit, limit.Number)
		if windowDesc != "" {
			displayName += " (" + windowDesc + ")"
		}

		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        name,
			DisplayName: displayName,
			Utilization: usedPct,
			ResetsAt:    resetsAt,
			Limit:       total,
			Used:        used,
		})
	}
	if len(data.Windows) == 0 {
		data.Error = "no complete quota data"
	}

	return data
}

// limitUsage follows CodexBar's contradiction-safe rule: when direct usage and
// remaining-derived usage disagree, retain the larger non-negative estimate.
func limitUsage(limit apiLimit) (used, total int) {
	if limit.Usage == nil || *limit.Usage <= 0 {
		return nonNegativeInt(pointerValue(limit.CurrentValue)), 0
	}
	total = int(*limit.Usage)
	current := nonNegativeInt(pointerValue(limit.CurrentValue))
	derived := 0
	if limit.Remaining != nil {
		derived = int(*limit.Usage - *limit.Remaining)
		if derived < 0 {
			derived = 0
		}
	}
	if derived > current {
		current = derived
	}
	if current > total {
		current = total
	}
	return current, total
}

func pointerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func nonNegativeInt(value int64) int {
	if value <= 0 {
		return 0
	}
	return int(value)
}

func tokenWindowName(limit apiLimit, index int) string {
	if limit.Unit == 6 && limit.Number == 1 {
		return "tokens_weekly"
	}
	if limit.Unit == 3 && limit.Number == 5 {
		return "tokens_5h"
	}
	return "tokens_" + strconv.Itoa(index)
}

func unitToString(unit, number int) string {
	switch unit {
	case 1:
		if number == 1 {
			return "daily"
		}
		return fmt.Sprintf("%dd", number)
	case 3:
		if number == 1 {
			return "hourly"
		}
		return fmt.Sprintf("%dh", number)
	case 5:
		return fmt.Sprintf("%dm", number)
	case 6:
		if number == 1 {
			return "weekly"
		}
		return fmt.Sprintf("%dw", number)
	default:
		return ""
	}
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("zai")
	return registry.Register(New(providerCfg))
}
