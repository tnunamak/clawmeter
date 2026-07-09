// Package xai implements the Provider interface for xAI/Grok API credits.
package xai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	managementBaseURL = "https://management-api.x.ai"
	timeout           = 10 * time.Second
)

// Provider implements the provider.Provider interface for xAI.
type Provider struct {
	cfg     config.ProviderConfig
	baseURL string
	client  *http.Client
}

// New creates a new xAI provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg:     cfg,
		baseURL: managementBaseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

func (p *Provider) Name() string         { return "xai" }
func (p *Provider) DisplayName() string  { return "Grok" }
func (p *Provider) Description() string  { return "xAI/Grok API credits (via XAI_MANAGEMENT_API_KEY)" }
func (p *Provider) DashboardURL() string { return "https://console.x.ai/team/default/billing" }
func (p *Provider) AutoPollByDefault() bool {
	return false
}

func (p *Provider) IsConfigured() bool {
	_, err := p.managementKey()
	return err == nil
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if _, err := p.managementKey(); err != nil {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "set XAI_MANAGEMENT_API_KEY; optionally set XAI_TEAM_ID to avoid an extra lookup",
		}
	}
	if _, err := p.configuredTeamID(); err == nil {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "management key and team id found"}
	}
	return provider.SetupStatus{State: provider.SetupReady, Detail: "management key found; team id will be discovered"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	key, err := p.managementKey()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	teamID, err := p.teamID(ctx, key)
	if err != nil {
		if isUnauthorized(err) {
			return &provider.UsageData{
				Provider:  p.Name(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "unauthorized — check XAI_MANAGEMENT_API_KEY",
			}, nil
		}
		return nil, err
	}

	resp, err := p.get(ctx, key, "/v1/billing/teams/"+url.PathEscape(teamID)+"/prepaid/balance")
	if err != nil {
		if isUnauthorized(err) {
			return &provider.UsageData{
				Provider:  p.Name(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "unauthorized — check XAI_MANAGEMENT_API_KEY",
			}, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	var balance prepaidBalanceResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&balance); err != nil {
		return nil, fmt.Errorf("decode prepaid balance: %w", err)
	}

	return p.transformBalance(&balance), nil
}

func (p *Provider) managementKey() (string, error) {
	if p.cfg.APIKey != "" {
		return strings.TrimSpace(p.cfg.APIKey), nil
	}
	if key := os.Getenv("XAI_MANAGEMENT_API_KEY"); strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), nil
	}
	return "", fmt.Errorf("no management key found")
}

func (p *Provider) configuredTeamID() (string, error) {
	if p.cfg.Extra != nil {
		if v, ok := p.cfg.Extra["team_id"]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s), nil
			}
		}
	}
	if id := os.Getenv("XAI_TEAM_ID"); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id), nil
	}
	return "", fmt.Errorf("no team id found")
}

func (p *Provider) teamID(ctx context.Context, key string) (string, error) {
	if id, err := p.configuredTeamID(); err == nil {
		return id, nil
	}

	resp, err := p.get(ctx, key, "/auth/management-keys/validation")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var validation managementKeyValidation
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&validation); err != nil {
		return "", fmt.Errorf("decode management key validation: %w", err)
	}
	if validation.TeamID != "" {
		return validation.TeamID, nil
	}
	if validation.Scope == "SCOPE_TEAM" && validation.ScopeID != "" {
		return validation.ScopeID, nil
	}
	return "", fmt.Errorf("management key is not team-scoped; set XAI_TEAM_ID")
}

func (p *Provider) get(ctx context.Context, key, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.baseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, unauthorizedError{status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}
	return resp, nil
}

type unauthorizedError struct {
	status int
}

func (e unauthorizedError) Error() string {
	return fmt.Sprintf("API returned %d", e.status)
}

func isUnauthorized(err error) bool {
	var unauthorized unauthorizedError
	return errors.As(err, &unauthorized)
}

type managementKeyValidation struct {
	TeamID  string `json:"teamId"`
	Scope   string `json:"scope"`
	ScopeID string `json:"scopeId"`
}

type prepaidBalanceResponse struct {
	Changes []balanceChange `json:"changes"`
	Total   centsValue      `json:"total"`
}

type balanceChange struct {
	ChangeOrigin string     `json:"changeOrigin"`
	TopupStatus  string     `json:"topupStatus"`
	Amount       centsValue `json:"amount"`
}

type centsValue struct {
	Val int64
}

func (c *centsValue) UnmarshalJSON(data []byte) error {
	var obj struct {
		Val json.RawMessage `json:"val"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if len(obj.Val) == 0 || string(obj.Val) == "null" {
		c.Val = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(obj.Val, &s); err == nil {
		return c.parse(s)
	}
	var n json.Number
	if err := json.Unmarshal(obj.Val, &n); err == nil {
		return c.parse(n.String())
	}
	return fmt.Errorf("invalid cents value")
}

func (c *centsValue) parse(s string) error {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fmt.Errorf("parse cents %q: %w", s, err)
	}
	c.Val = v
	return nil
}

func (p *Provider) transformBalance(resp *prepaidBalanceResponse) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0, 1),
	}

	availableCents := -resp.Total.Val
	if availableCents < 0 {
		availableCents = 0
	}

	issuedCents := int64(0)
	for _, change := range resp.Changes {
		if change.Amount.Val >= 0 {
			continue
		}
		if change.TopupStatus != "" && change.TopupStatus != "SUCCEEDED" {
			continue
		}
		issuedCents += -change.Amount.Val
	}

	limitCents := issuedCents
	usedCents := issuedCents - availableCents
	if usedCents < 0 {
		usedCents = 0
	}
	if limitCents == 0 {
		limitCents = availableCents
	}

	utilization := 0.0
	if limitCents > 0 {
		utilization = float64(usedCents) / float64(limitCents) * 100
	}
	if availableCents == 0 && issuedCents > 0 {
		utilization = 100
	}
	if utilization < 0 {
		utilization = 0
	}
	if utilization > 100 {
		utilization = 100
	}

	data.Windows = append(data.Windows, provider.UsageWindow{
		Name:        "credits",
		DisplayName: "Prepaid credits",
		Utilization: utilization,
		// xAI API credits do not have a reset window. Match existing credit
		// providers with a far-future reset so the shared UI can display them.
		ResetsAt: time.Now().Add(365 * 24 * time.Hour),
		Limit:    clampInt(limitCents),
		Used:     clampInt(usedCents),
	})

	return data
}

func clampInt(v int64) int {
	if v > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(v)
}

// Register registers the xAI provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("xai")
	return registry.Register(New(providerCfg))
}
