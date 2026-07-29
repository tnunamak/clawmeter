// Package alibaba implements the Provider interface for Alibaba Cloud Model
// Studio Coding Plan quotas (Qwen / DashScope).
package alibaba

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	intlHost = "https://modelstudio.console.alibabacloud.com"
	cnHost   = "https://bailian.console.aliyun.com"

	intlRegionID = "ap-southeast-1"
	cnRegionID   = "cn-beijing"

	intlCommodityCode = "sfm_codingplan_public_intl"
	cnCommodityCode   = "sfm_codingplan_public_cn"

	quotaAction  = "zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2"
	quotaAPIName = "queryCodingPlanInstanceInfoV2"

	timeout     = 12 * time.Second
	maxBodySize = 2 << 20 // 2 MiB
)

// envVarNames are checked in priority order for the Coding Plan API key.
var envVarNames = []string{
	"ALIBABA_CODING_PLAN_API_KEY",
	"BAILIAN_CODING_PLAN_API_KEY",
	"ALIBABA_QWEN_API_KEY",
	"DASHSCOPE_API_KEY",
}

type Provider struct {
	cfg      config.ProviderConfig
	client   *http.Client
	usageURL string // overridable for tests; empty means build from region
	now      func() time.Time
}

func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				req.Header.Del("Authorization")
				req.Header.Del("x-api-key")
				req.Header.Del("X-DashScope-API-Key")
				return nil
			},
		},
		now: time.Now,
	}
}

func (p *Provider) Name() string             { return "alibaba" }
func (p *Provider) DisplayName() string      { return "Alibaba" }
func (p *Provider) Description() string      { return "Alibaba Cloud Model Studio Coding Plan" }
func (p *Provider) DashboardURL() string     { return "https://modelstudio.console.alibabacloud.com" }
func (p *Provider) SafeForAutoPolling() bool { return true }

func (p *Provider) IsConfigured() bool {
	key, _ := p.apiKey()
	return key != ""
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if key, _ := p.apiKey(); key != "" {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "API key found"}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "set ALIBABA_CODING_PLAN_API_KEY"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	key, err := p.apiKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	data, err := p.fetchRegion(ctx, key, intlHost, intlRegionID, intlCommodityCode)
	if err != nil && shouldRetryRegion(err) {
		data, err = p.fetchRegion(ctx, key, cnHost, cnRegionID, cnCommodityCode)
	}
	if err != nil && isAuthFailure(err) {
		return &provider.UsageData{
			Provider:              p.Name(),
			FetchedAt:             p.now(),
			IsExpired:             true,
			InvalidatesPriorUsage: true,
			Error:                 "unauthorized — check API key",
		}, nil
	}
	return data, err
}

// regionError wraps fetch errors with a flag for region-retry eligibility.
type regionError struct {
	err         error
	retryable   bool
	authFailure bool
}

func (e *regionError) Error() string { return e.err.Error() }
func (e *regionError) Unwrap() error { return e.err }

func shouldRetryRegion(err error) bool {
	var re *regionError
	if ok := asRegionError(err, &re); ok {
		return re.retryable
	}
	return false
}

func isAuthFailure(err error) bool {
	var re *regionError
	if ok := asRegionError(err, &re); ok {
		return re.authFailure
	}
	return false
}

func asRegionError(err error, target **regionError) bool {
	for err != nil {
		if re, ok := err.(*regionError); ok {
			*target = re
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}

func (p *Provider) fetchRegion(ctx context.Context, key, host, regionID, commodityCode string) (*provider.UsageData, error) {
	endpoint := p.usageURL
	if endpoint == "" {
		endpoint = fmt.Sprintf(
			"%s/data/api.json?action=%s&product=broadscope-bailian&api=%s&currentRegionId=%s",
			host, quotaAction, quotaAPIName, regionID,
		)
	}

	body, _ := json.Marshal(map[string]any{
		"queryCodingPlanInstanceInfoRequest": map[string]string{
			"commodityCode": commodityCode,
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("x-api-key", key)
	req.Header.Set("X-DashScope-API-Key", key)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, &regionError{err: fmt.Errorf("request failed: %w", err), retryable: false}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &regionError{
			err:         fmt.Errorf("unauthorized (HTTP %d) — key may not be valid in this region", resp.StatusCode),
			retryable:   true,
			authFailure: true,
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, &regionError{err: fmt.Errorf("API returned 404"), retryable: true}
	}

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodySize))
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	parsed = expandJSON(parsed)

	if isLoginRequired(parsed) {
		return nil, &regionError{err: fmt.Errorf("login required (key may not be valid in this region)"), retryable: true}
	}

	quota := findQuotaInfo(parsed)
	if quota == nil {
		return nil, &regionError{err: fmt.Errorf("no coding plan quota data found"), retryable: true}
	}

	return p.transformQuota(quota), nil
}

func (p *Provider) transformQuota(quota map[string]any) *provider.UsageData {
	now := p.now()
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: now,
		Windows:   make([]provider.UsageWindow, 0, 3),
	}

	type windowDef struct {
		name        string
		displayName string
		usedKeys    []string
		totalKeys   []string
		resetKeys   []string
	}
	windows := []windowDef{
		{
			name:        "session_5h",
			displayName: "5-Hour",
			usedKeys:    []string{"per5HourUsedQuota", "perFiveHourUsedQuota"},
			totalKeys:   []string{"per5HourTotalQuota", "perFiveHourTotalQuota"},
			resetKeys:   []string{"per5HourQuotaNextRefreshTime", "perFiveHourQuotaNextRefreshTime"},
		},
		{
			name:        "weekly",
			displayName: "Weekly",
			usedKeys:    []string{"perWeekUsedQuota"},
			totalKeys:   []string{"perWeekTotalQuota"},
			resetKeys:   []string{"perWeekQuotaNextRefreshTime"},
		},
		{
			name:        "monthly",
			displayName: "Monthly",
			usedKeys:    []string{"perBillMonthUsedQuota", "perMonthUsedQuota"},
			totalKeys:   []string{"perBillMonthTotalQuota", "perMonthTotalQuota"},
			resetKeys:   []string{"perBillMonthQuotaNextRefreshTime", "perMonthQuotaNextRefreshTime"},
		},
	}

	for _, wd := range windows {
		used, usedOK := lookupNumber(quota, wd.usedKeys)
		total, totalOK := lookupNumber(quota, wd.totalKeys)
		if !usedOK || !totalOK || total <= 0 {
			continue
		}
		if used < 0 {
			used = 0
		}
		if used > total {
			used = total
		}
		utilization := used / total * 100

		var resetsAt time.Time
		if reset, ok := lookupAny(quota, wd.resetKeys...); ok {
			resetsAt = parseTimestamp(reset)
		}

		// Normalize 5-hour reset: if <60s away, push forward by 5 hours.
		if wd.name == "session_5h" && !resetsAt.IsZero() && resetsAt.Sub(now) < 60*time.Second {
			resetsAt = resetsAt.Add(5 * time.Hour)
			if resetsAt.Sub(now) < 60*time.Second {
				resetsAt = now.Add(5 * time.Hour)
			}
		}

		data.Windows = append(data.Windows, provider.UsageWindow{
			Name:        wd.name,
			DisplayName: wd.displayName,
			Utilization: math.Round(utilization*100) / 100,
			ResetsAt:    resetsAt,
			Limit:       int(total),
			Used:        int(used),
		})
	}

	if len(data.Windows) == 0 {
		data.Error = "no complete quota data"
	}
	return data
}

// apiKey resolves the Coding Plan API key from config, environment, or
// Qwen Code's settings.json.
func (p *Provider) apiKey() (string, error) {
	if p.cfg.APIKey != "" {
		return p.cfg.APIKey, nil
	}
	for _, name := range envVarNames {
		if key := strings.Trim(strings.TrimSpace(os.Getenv(name)), `"' `); key != "" {
			return key, nil
		}
	}
	if key := qwenSettingsKey(); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("no API key found (set ALIBABA_CODING_PLAN_API_KEY)")
}

// qwenSettingsKey reads the API key from ~/.qwen/settings.json env block.
func qwenSettingsKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return ""
	}
	for _, key := range []string{"BAILIAN_CODING_PLAN_API_KEY", "BAILIAN_TOKEN_PLAN_API_KEY"} {
		if v := strings.TrimSpace(settings.Env[key]); v != "" {
			return v
		}
	}
	return ""
}

// expandJSON recursively parses JSON-encoded string values, handling
// double-encoded payloads from the Alibaba console gateway.
func expandJSON(v any) any {
	switch val := v.(type) {
	case string:
		trimmed := strings.TrimSpace(val)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			var inner any
			if json.Unmarshal([]byte(trimmed), &inner) == nil {
				return expandJSON(inner)
			}
		}
		return val
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			out[k] = expandJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = expandJSON(child)
		}
		return out
	default:
		return v
	}
}

// findRecursive searches nested maps/slices for the first value at any of the
// given keys.
func findRecursive(v any, keys ...string) (any, bool) {
	for _, key := range keys {
		if found := searchKey(v, key); found != nil {
			return found, true
		}
	}
	return nil, false
}

func searchKey(v any, key string) any {
	switch val := v.(type) {
	case map[string]any:
		if hit, ok := val[key]; ok {
			return hit
		}
		for _, child := range val {
			if hit := searchKey(child, key); hit != nil {
				return hit
			}
		}
	case []any:
		for _, child := range val {
			if hit := searchKey(child, key); hit != nil {
				return hit
			}
		}
	}
	return nil
}

// findQuotaInfo locates the codingPlanQuotaInfo dict within the response.
// When an instance list is present, quota is read only from the selected
// instance — never from sibling instances that may be expired.
func findQuotaInfo(v any) map[string]any {
	instances := searchKey(v, "codingPlanInstanceInfos")
	if instances == nil {
		instances = searchKey(v, "coding_plan_instance_infos")
	}
	if arr, ok := instances.([]any); ok && len(arr) > 0 {
		best := selectInstance(arr)
		if m, ok := best.(map[string]any); ok {
			if q := searchKey(m, "codingPlanQuotaInfo"); q != nil {
				if qm, ok := q.(map[string]any); ok {
					return qm
				}
			}
			if q := searchKey(m, "coding_plan_quota_info"); q != nil {
				if qm, ok := q.(map[string]any); ok {
					return qm
				}
			}
		}
		return nil
	}
	// No instance list — search the whole response.
	if q := searchKey(v, "codingPlanQuotaInfo"); q != nil {
		if qm, ok := q.(map[string]any); ok {
			return qm
		}
	}
	if q := searchKey(v, "coding_plan_quota_info"); q != nil {
		if qm, ok := q.(map[string]any); ok {
			return qm
		}
	}
	if m, ok := v.(map[string]any); ok {
		if _, has := m["per5HourUsedQuota"]; has {
			return m
		}
		if _, has := m["perFiveHourUsedQuota"]; has {
			return m
		}
	}
	return nil
}

// selectInstance scores instances and returns the best candidate.
func selectInstance(arr []any) any {
	best := arr[0]
	bestScore := scoreInstance(arr[0])
	for _, item := range arr[1:] {
		if s := scoreInstance(item); s > bestScore {
			best = item
			bestScore = s
		}
	}
	return best
}

func scoreInstance(v any) int {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	score := 0
	status, _ := lookupString(m, "status", "instanceStatus")
	switch strings.ToUpper(status) {
	case "VALID", "ACTIVE":
		score += 3
	case "EXPIRED", "INVALID", "DISABLED", "RELEASED":
		score -= 1
	}
	if active, ok := lookupBool(m, "isActive", "active"); ok && active {
		score += 3
	}
	if end, ok := lookupAny(m, "endTime", "periodEndTime", "expireTime", "expirationTime"); ok {
		if t := parseTimestamp(end); !t.IsZero() && t.After(time.Now()) {
			score++
		}
	}
	return score
}

func isLoginRequired(v any) bool {
	for _, key := range []string{"code", "status", "statusCode"} {
		if s := searchKey(v, key); s != nil {
			if str, ok := s.(string); ok {
				lower := strings.ToLower(str)
				if strings.Contains(lower, "needlogin") || strings.Contains(lower, "login") {
					return true
				}
			}
		}
	}
	return false
}

// Lookup helpers

func lookupNumber(m map[string]any, keys []string) (float64, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch n := v.(type) {
			case float64:
				return n, true
			case int:
				return float64(n), true
			case int64:
				return float64(n), true
			case json.Number:
				if f, err := n.Float64(); err == nil {
					return f, true
				}
			}
		}
	}
	return 0, false
}

func lookupAny(m map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v, true
		}
	}
	return nil, false
}

func lookupString(m map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func lookupBool(m map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if b, ok := v.(bool); ok {
				return b, true
			}
		}
	}
	return false, false
}

// parseTimestamp handles millisecond epoch, second epoch, and ISO 8601 strings.
func parseTimestamp(v any) time.Time {
	switch val := v.(type) {
	case float64:
		return epochToTime(val)
	case int:
		return epochToTime(float64(val))
	case int64:
		return epochToTime(float64(val))
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return epochToTime(f)
		}
	case string:
		val = strings.TrimSpace(val)
		if val == "" {
			return time.Time{}
		}
		for _, layout := range []string{
			time.RFC3339,
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02 15:04",
		} {
			if t, err := time.Parse(layout, val); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func epochToTime(f float64) time.Time {
	if f <= 0 {
		return time.Time{}
	}
	if f > 1e12 {
		return time.UnixMilli(int64(f))
	}
	if f > 1e9 {
		return time.Unix(int64(f), 0)
	}
	return time.Time{}
}

func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("alibaba")
	return registry.Register(New(providerCfg))
}
