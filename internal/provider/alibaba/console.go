package alibaba

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	codingPlanConsoleOperation = "zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2"
	defaultConsoleRegion       = "cn-beijing"
	defaultConsoleSite         = "domestic"
)

// consoleSession is the read-only console OAuth session written by the
// official Model Studio CLI. Coding Plan deliberately does not inspect browser
// cookies or the separate Personal Token Plan profile.
type consoleSession struct {
	accessToken string
	region      string
	site        string
	switchAgent any
}

func codingPlanConsoleConfigPaths() []string {
	paths := make([]string, 0, 2)
	if configDir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(configDir, "clawmeter", "alibaba-coding-plan", "config.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".bailian", "config.json"))
	}
	return paths
}

func (p *Provider) consoleSession() (consoleSession, bool) {
	paths := codingPlanConsoleConfigPaths()
	if p.consoleConfigPath != "" {
		paths = []string{p.consoleConfigPath}
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
		region = defaultConsoleRegion
	}
	site, _ := selected["console_site"].(string)
	if site != "international" {
		site = defaultConsoleSite
	}
	return consoleSession{accessToken: strings.TrimSpace(token), region: region, site: site, switchAgent: selected["console_switch_agent"]}, true
}

func (p *Provider) fetchConsoleUsage(ctx context.Context, session consoleSession) (*provider.UsageData, error) {
	commodityCode := intlCommodityCode
	if session.site != "international" {
		commodityCode = cnCommodityCode
	}
	result, err := p.callConsole(ctx, session, commodityCode)
	if err != nil {
		return nil, err
	}
	if isLoginRequired(result) {
		return nil, fmt.Errorf("console login required")
	}
	if err := consoleResponseError(result); err != nil {
		return nil, err
	}
	quota := findQuotaInfo(result)
	if quota == nil {
		// An authenticated account without this separate subscription should not
		// look like a broken provider or a failed credential.
		return &provider.UsageData{Provider: p.Name(), FetchedAt: p.now()}, nil
	}
	return p.transformQuota(quota), nil
}

func consoleResponseError(result any) error {
	root, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	payload, ok := root["data"].(map[string]any)
	if !ok {
		return nil
	}
	if success, ok := payload["success"].(bool); ok && !success {
		if code, _ := payload["errorCode"].(string); code != "" {
			return fmt.Errorf("console query rejected: %s", code)
		}
		return fmt.Errorf("console query rejected")
	}
	return nil
}

// callConsole permits exactly the documented read-only quota query. It cannot
// be repurposed for purchase, plan mutation, or any other console operation.
func (p *Provider) callConsole(ctx context.Context, session consoleSession, commodityCode string) (any, error) {
	endpoint := p.consoleEndpoint
	if endpoint == "" {
		host, action := consoleGateway(session.region, session.site)
		endpoint = fmt.Sprintf("https://%s/cli/api.json?action=%s&product=sfm_bailian&api=%s", host, action, url.QueryEscape(codingPlanConsoleOperation))
	}

	cornerstone := map[string]any{
		"protocol":       "V2",
		"console":        "ONE_CONSOLE",
		"productCode":    "p_efm",
		"switchUserType": 3,
		"consoleSite":    "BAILIAN_ALIYUN",
	}
	if session.switchAgent != nil {
		cornerstone["switchAgent"] = session.switchAgent
	}
	params, err := json.Marshal(map[string]any{
		"Api": codingPlanConsoleOperation,
		"V":   "1.0",
		"Data": map[string]any{
			"queryCodingPlanInstanceInfoRequest": map[string]any{
				"commodityCode": commodityCode,
				"onlyLatestOne": true,
			},
			"cornerstoneParam": cornerstone,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode console request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{
		"params": {string(params)},
		"region": {session.region},
	}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+session.accessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("console request failed: %w", err)
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
	return expandJSON(result), nil
}

func consoleGateway(region, site string) (host, action string) {
	if region == intlRegionID {
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

func (p *Provider) consoleErrorData(err error) *provider.UsageData {
	message := "Coding Plan usage unavailable"
	expired := false
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "login") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") {
		message = "Model Studio console session expired — run `bl auth login --console`"
		if p.explicitSource {
			message = "enrolled Model Studio console session expired"
		}
		expired = true
	}
	return &provider.UsageData{Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(), FetchedAt: p.now(), IsExpired: expired, InvalidatesPriorUsage: expired, Error: message}
}
