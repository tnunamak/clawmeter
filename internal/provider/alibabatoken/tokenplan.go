// Package alibabatoken reads Alibaba Personal Token Plan usage through a
// read-only Model Studio console session.
package alibabatoken

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	name        = "alibaba_token"
	displayName = "Alibaba Token Plan"

	defaultRegion = "cn-beijing"
	defaultSite   = "domestic"
	maxBodySize   = 2 << 20

	domesticDashboardURL = "https://bailian.console.aliyun.com/cn-beijing?tab=plan#/efm/subscription/overview"
)

// These are the Personal Token Plan console operations observed in Alibaba's
// public console bundle. They are intentionally explicit so a future endpoint
// change cannot accidentally reach the reset-card redemption operation.
const (
	usageOperation      = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"
	resetCardsOperation = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/reset-card/list"
)

var tokenPlanEnvNames = []string{"BAILIAN_TOKEN_PLAN_API_KEY"}

type Provider struct {
	cfg        config.ProviderConfig
	client     *http.Client
	now        func() time.Time
	configPath string
	endpoint   string

	sessionEnvironmentResolver provider.SessionEnvironmentResolver
	sourceID                   string
	sourceLabel                string
	explicitSource             bool
	enrolledSource             bool
}

// consoleConfigPaths treats the official CLI's store as authoritative after a
// reconnect, with Clawmeter's dedicated store as a compatibility fallback.
// Neither store contains the model-inference API key; the quota session is a
// separate browser-authorized credential.
func consoleConfigPaths() []string {
	paths := make([]string, 0, 2)
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".bailian", "config.json"))
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(configDir, "clawmeter", "alibaba-token-plan", "config.json"))
	}
	return paths
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg:    cfg,
		client: &http.Client{Timeout: 12 * time.Second},
		now:    time.Now,
	}
}

func (p *Provider) Name() string        { return name }
func (p *Provider) DisplayName() string { return displayName }
func (p *Provider) Description() string {
	return "Alibaba Personal Token Plan (via Model Studio console login)"
}
func (p *Provider) DashboardURL() string {
	if session, ok := p.consoleSession(); ok && session.site == "international" {
		return internationalDashboardURL
	}
	return domesticDashboardURL
}

const internationalDashboardURL = "https://modelstudio.console.alibabacloud.com/?tab=plan#/efm/subscription/token-plan/personal"

func (p *Provider) SafeForAutoPolling() bool { return true }
func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
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
		{Kind: "console-file", Summary: "Exact Model Studio console profile file", RefUsage: "/absolute/path/config.json", RefRequired: true, RefIsPath: true},
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
			return fmt.Errorf("provider %q source %q cannot use native credentials", name, source.ID)
		}
	case "console-file":
		if ref == "" || !filepath.IsAbs(ref) {
			return fmt.Errorf("provider %q source %q requires an absolute console file", name, source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", name, source.ID, kind)
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
	p.enrolledSource = true
	if source.Credential.Kind == "console-file" {
		p.configPath = strings.TrimSpace(source.Credential.Ref)
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
	material := p.configPath
	if info, err := os.Stat(p.configPath); err == nil {
		material += fmt.Sprintf("\x00%d\x00%d\x00%d", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm())
	} else {
		material += "\x00unavailable"
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(material)))
}

func (p *Provider) withSource(data *provider.UsageData) *provider.UsageData {
	if data != nil {
		data.Provider, data.SourceID, data.SourceLabel = p.Name(), p.SourceID(), p.SourceLabel()
	}
	return data
}

func (p *Provider) IsConfigured() bool {
	_, ok := p.consoleSession()
	return ok
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if _, ok := p.consoleSession(); ok {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "Model Studio console session found"}
	}
	if p.explicitSource {
		return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "enrolled Model Studio console session is unavailable"}
	}
	if _, err := exec.LookPath("bl"); err == nil {
		return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "Bailian CLI found; connect quota access with `clawmeter providers connect token-plan`"}
	}
	if p.hasTokenPlanKey() {
		return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "Token Plan key found; connect quota access with `clawmeter providers connect token-plan`"}
	}
	return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "no Token Plan key or console session; connect quota access with `clawmeter providers connect token-plan`"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	session, ok := p.consoleSession()
	if !ok {
		message := "Model Studio quota access is not connected; run `clawmeter providers connect token-plan`"
		if p.explicitSource {
			message = "enrolled Model Studio console session is unavailable"
		}
		return p.withSource(&provider.UsageData{Provider: p.Name(), FetchedAt: p.now(), IsExpired: true, InvalidatesPriorUsage: true, Error: message}), nil
	}

	usage, err := p.call(ctx, session, usageOperation)
	if err != nil {
		return p.withSource(p.errorData(err)), nil
	}
	data, err := parseUsage(usage, p.now())
	if err != nil {
		return nil, err
	}
	p.withSource(data)

	cards, err := p.call(ctx, session, resetCardsOperation)
	if err == nil {
		data.ResetCredits = parseResetCards(cards, p.now())
	}
	return data, nil
}

func (p *Provider) errorData(err error) *provider.UsageData {
	message := "Token Plan usage unavailable"
	expired := false
	if strings.Contains(strings.ToLower(err.Error()), "login") || strings.Contains(strings.ToLower(err.Error()), "unauthorized") || strings.Contains(strings.ToLower(err.Error()), "forbidden") {
		message = "Model Studio quota access expired; run `clawmeter providers connect token-plan --force`"
		if p.explicitSource {
			message = "enrolled Model Studio console session expired"
		}
		expired = true
	}
	return &provider.UsageData{Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(), FetchedAt: p.now(), IsExpired: expired, InvalidatesPriorUsage: expired, Error: message}
}

type consoleSession struct {
	accessToken string
	region      string
	site        string
	switchAgent any
}

func (p *Provider) consoleSession() (consoleSession, bool) {
	paths := consoleConfigPaths()
	if p.configPath != "" {
		paths = []string{p.configPath}
	}
	for _, path := range paths {
		if session, ok := readConsoleSession(path); ok {
			return session, true
		}
	}
	return consoleSession{}, false
}

func readConsoleSession(path string) (consoleSession, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return consoleSession{}, false
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return consoleSession{}, false
	}
	selected := root
	if active, _ := root["active_config"].(string); active != "" && active != "default" {
		if profile, ok := root[active].(map[string]any); ok {
			selected = profile
		}
	}
	token, _ := selected["access_token"].(string)
	if strings.TrimSpace(token) == "" {
		return consoleSession{}, false
	}
	region, _ := selected["console_region"].(string)
	if region == "" {
		region = defaultRegion
	}
	site, _ := selected["console_site"].(string)
	if site != "international" {
		site = defaultSite
	}
	return consoleSession{accessToken: strings.TrimSpace(token), region: region, site: site, switchAgent: selected["console_switch_agent"]}, true
}

func (p *Provider) hasTokenPlanKey() bool {
	for _, envName := range tokenPlanEnvNames {
		if strings.HasPrefix(strings.TrimSpace(os.Getenv(envName)), "sk-sp-") {
			return true
		}
	}
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: append([]string(nil), tokenPlanEnvNames...), AllowSessionEnvironmentFallback: true})
		for _, envName := range tokenPlanEnvNames {
			if strings.HasPrefix(strings.TrimSpace(values[envName]), "sk-sp-") {
				return true
			}
		}
	}
	return false
}

func (p *Provider) call(ctx context.Context, session consoleSession, operation string) (any, error) {
	if operation != usageOperation && operation != resetCardsOperation {
		return nil, fmt.Errorf("refusing non-read-only Token Plan operation")
	}
	endpoint := p.endpoint
	if endpoint == "" {
		host, action := gateway(session.region, session.site)
		endpoint = fmt.Sprintf("https://%s/cli/api.json?action=%s&product=sfm_bailian&api=%s", host, action, url.QueryEscape(operation))
	}
	data := map[string]any{"cornerstoneParam": map[string]any{"protocol": "V2", "console": "ONE_CONSOLE", "productCode": "p_efm", "switchUserType": 3, "consoleSite": "BAILIAN_ALIYUN"}}
	if session.switchAgent != nil {
		data["cornerstoneParam"].(map[string]any)["switchAgent"] = session.switchAgent
	}
	params, _ := json.Marshal(map[string]any{"Api": operation, "V": "1.0", "Data": data})
	form := url.Values{"params": {string(params)}, "region": {session.region}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+session.accessToken)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("console login rejected")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("console gateway returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, err
	}
	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode console response: %w", err)
	}
	if containsLoginError(result) {
		return nil, fmt.Errorf("console login required")
	}
	return expandJSON(result), nil
}

func gateway(region, site string) (host, action string) {
	if region == "ap-southeast-1" {
		if site == "international" {
			return "bailian-singapore-cs.alibabacloud.com", "IntlBroadScopeAspnGateway"
		}
		return "modelstudio-cs.console.aliyun.com", "IntlBroadScopeAspnGateway"
	}
	if site == "international" {
		return "bailian-cs.console.alibabacloud.com", "BroadScopeAspnGateway"
	}
	return "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway"
}

func parseUsage(raw any, now time.Time) (*provider.UsageData, error) {
	result := &provider.UsageData{FetchedAt: now}
	for _, spec := range []struct{ name, display, pct, reset string }{
		{"session_5h", "5h", "per5HourPercentage", "per5HourResetTime"},
		{"weekly", "7d", "per1WeekPercentage", "per1WeekResetTime"},
	} {
		utilization, ok := findNumber(raw, spec.pct)
		if !ok {
			continue
		}
		reset, _ := findValue(raw, spec.reset)
		result.Windows = append(result.Windows, provider.UsageWindow{Name: spec.name, DisplayName: spec.display, Utilization: normalizeUtilization(utilization), ResetsAt: parseTime(reset)})
	}
	if len(result.Windows) == 0 {
		return nil, fmt.Errorf("no Personal Token Plan quota data found")
	}
	return result, nil
}

// The Personal Token Plan console currently returns utilization as a fraction
// (for example 0.2561 means 25.61%). Accept percentage points as a defensive
// fallback if Alibaba changes the undocumented console contract.
func normalizeUtilization(value float64) float64 {
	if value >= 0 && value <= 1 {
		value *= 100
	}
	return math.Max(0, math.Min(100, value))
}

func parseResetCards(raw any, now time.Time) *provider.UsageResetCredits {
	items, _ := findValue(raw, "resetCards")
	if items == nil {
		items, _ = findValue(raw, "list")
	}
	array, _ := items.([]any)
	credits := make([]provider.UsageResetCredit, 0, len(array))
	for _, item := range array {
		status, _ := findValue(item, "status")
		expires, _ := findValue(item, "expiresAt")
		if expires == nil {
			expires, _ = findValue(item, "expireTime")
		}
		credit := provider.UsageResetCredit{Status: strings.ToLower(fmt.Sprint(status)), ExpiresAt: parseTime(expires)}
		if credit.Status == "available" && (credit.ExpiresAt.IsZero() || credit.ExpiresAt.After(now)) {
			credits = append(credits, credit)
		}
	}
	if len(credits) == 0 {
		return nil
	}
	sort.SliceStable(credits, func(i, j int) bool { return credits[i].ExpiresAt.Before(credits[j].ExpiresAt) })
	return &provider.UsageResetCredits{AvailableCount: len(credits), Credits: credits, FetchedAt: now}
}

func findValue(value any, key string) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if found, ok := typed[key]; ok {
			return found, true
		}
		for _, child := range typed {
			if found, ok := findValue(child, key); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range typed {
			if found, ok := findValue(child, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func findNumber(value any, key string) (float64, bool) {
	found, ok := findValue(value, key)
	if !ok {
		return 0, false
	}
	switch n := found.(type) {
	case float64:
		return n, true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	case string:
		v, err := strconv.ParseFloat(n, 64)
		return v, err == nil
	}
	return 0, false
}

func parseTime(value any) time.Time {
	switch t := value.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
			if v, err := time.Parse(layout, t); err == nil {
				return v
			}
		}
	case float64:
		return time.UnixMilli(int64(t))
	}
	return time.Time{}
}

func containsLoginError(value any) bool {
	text := strings.ToLower(fmt.Sprint(value))
	return strings.Contains(text, "consoleneedlogin") || strings.Contains(text, "notlogined")
}

func expandJSON(value any) any {
	switch typed := value.(type) {
	case string:
		var decoded any
		if json.Unmarshal([]byte(typed), &decoded) == nil {
			return expandJSON(decoded)
		}
		return typed
	case map[string]any:
		for key, child := range typed {
			typed[key] = expandJSON(child)
		}
	case []any:
		for index, child := range typed {
			typed[index] = expandJSON(child)
		}
	}
	return value
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider(name)
	return registry.Register(New(providerCfg))
}
