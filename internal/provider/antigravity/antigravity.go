// Package antigravity implements quota monitoring for the Antigravity CLI.
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	defaultBaseURL     = "https://daily-cloudcode-pa.googleapis.com"
	loadCodeAssistPath = "/v1internal:loadCodeAssist"
	quotaSummaryPath   = "/v1internal:retrieveUserQuotaSummary"
	requestTimeout     = 12 * time.Second
	maxResponseBytes   = 1 << 20
)

type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Provider reads the Antigravity CLI's existing login and fetches its two
// provider-reported weekly usage pools.
type Provider struct {
	baseURL  string
	client   httpClient
	homeDir  func() (string, error)
	lookPath func(string) (string, error)
	runCLI   func(context.Context) error
	now      func() time.Time
}

// New creates an Antigravity provider.
func New() *Provider {
	p := &Provider{
		baseURL:  defaultBaseURL,
		client:   &http.Client{Timeout: requestTimeout},
		homeDir:  os.UserHomeDir,
		lookPath: exec.LookPath,
		now:      time.Now,
	}
	p.runCLI = p.runModels
	return p
}

func (p *Provider) Name() string        { return "antigravity" }
func (p *Provider) DisplayName() string { return "Antigravity" }
func (p *Provider) Description() string {
	return "Google Antigravity weekly model pools (via agy login)"
}
func (p *Provider) DashboardURL() string { return "https://antigravity.google" }
func (p *Provider) AutoPollByDefault() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) IsConfigured() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if _, err := p.lookPath("agy"); err != nil {
		return provider.SetupStatus{
			State:  provider.SetupUnavailable,
			Detail: "Antigravity CLI is not installed",
		}
	}
	token, err := p.readToken()
	if errors.Is(err, errTokenPermissions) {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "Antigravity login file must be accessible only to its owner",
		}
	}
	if errors.Is(err, errTokenMalformed) {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "Antigravity login is unreadable; run `agy` to sign in again",
		}
	}
	if err != nil || token.AccessToken == "" {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "run `agy` to sign in",
		}
	}
	return provider.SetupStatus{State: provider.SetupReady, Detail: "Antigravity CLI login found"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	token, err := p.validAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	project, err := p.loadProject(ctx, token)
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return expiredUsage(p.now()), nil
		}
		return nil, err
	}
	body, err := p.postJSON(ctx, quotaSummaryPath, token, map[string]string{"project": project})
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return expiredUsage(p.now()), nil
		}
		return nil, err
	}

	windows, err := parseQuotaSummary(body, p.now())
	if err != nil {
		return nil, err
	}
	return &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: p.now(),
		Windows:   windows,
	}, nil
}

type storedToken struct {
	AccessToken string
	Expiry      time.Time
}

type tokenFile struct {
	Token struct {
		AccessToken string    `json:"access_token"`
		Expiry      time.Time `json:"expiry"`
	} `json:"token"`
}

func (p *Provider) validAccessToken(ctx context.Context) (string, error) {
	token, err := p.readToken()
	if err == nil && token.AccessToken != "" && token.Expiry.After(p.now().Add(time.Minute)) {
		return token.AccessToken, nil
	}

	refreshCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := p.runCLI(refreshCtx); err != nil {
		return "", fmt.Errorf("Antigravity login unavailable; run `agy` to sign in")
	}
	token, err = p.readToken()
	if errors.Is(err, errTokenPermissions) {
		return "", fmt.Errorf("Antigravity login file must be accessible only to its owner")
	}
	if errors.Is(err, errTokenMalformed) {
		return "", fmt.Errorf("Antigravity login is unreadable; run `agy` to sign in again")
	}
	if err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("Antigravity login is not available to Clawmeter; run `agy` to sign in")
	}
	if token.Expiry.IsZero() || !token.Expiry.After(p.now().Add(time.Minute)) {
		return "", fmt.Errorf("Antigravity login expired; run `agy` to sign in")
	}
	return token.AccessToken, nil
}

var (
	errTokenPermissions = errors.New("Antigravity login permissions are too broad")
	errTokenMalformed   = errors.New("Antigravity login is malformed")
)

func (p *Provider) readToken() (storedToken, error) {
	home, err := p.homeDir()
	if err != nil {
		return storedToken{}, fmt.Errorf("locate Antigravity login: %w", err)
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	info, err := os.Stat(path)
	if err != nil {
		return storedToken{}, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return storedToken{}, errTokenPermissions
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return storedToken{}, err
	}
	var stored tokenFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return storedToken{}, fmt.Errorf("%w: %v", errTokenMalformed, err)
	}
	return storedToken{
		AccessToken: strings.TrimSpace(stored.Token.AccessToken),
		Expiry:      stored.Token.Expiry,
	}, nil
}

func (p *Provider) runModels(ctx context.Context) error {
	path, err := p.lookPath("agy")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, "models")
	hideSubprocessWindow(cmd)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

var errUnauthorized = errors.New("unauthorized")

func (p *Provider) loadProject(ctx context.Context, token string) (string, error) {
	body, err := p.postJSON(ctx, loadCodeAssistPath, token, map[string]any{
		"metadata": map[string]string{"ideType": "ANTIGRAVITY"},
	})
	if err != nil {
		return "", err
	}
	var response struct {
		Project string `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode Antigravity account status: %w", err)
	}
	if strings.TrimSpace(response.Project) == "" {
		return "", fmt.Errorf("Antigravity account status did not include a quota project")
	}
	return strings.TrimSpace(response.Project), nil
}

func (p *Provider) postJSON(ctx context.Context, path, token string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Antigravity request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Clawmeter Antigravity quota monitor")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Antigravity request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Antigravity response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, fmt.Errorf("Antigravity response exceeded size limit")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Antigravity API returned HTTP %d", resp.StatusCode)
	}
	return responseBody, nil
}

type quotaSummary struct {
	Groups []quotaGroup `json:"groups"`
}

type quotaGroup struct {
	DisplayName string        `json:"displayName"`
	Buckets     []quotaBucket `json:"buckets"`
}

type quotaBucket struct {
	BucketID          string   `json:"bucketId"`
	DisplayName       string   `json:"displayName"`
	RemainingFraction *float64 `json:"remainingFraction"`
	Remaining         *struct {
		RemainingFraction *float64 `json:"remainingFraction"`
	} `json:"remaining"`
	ResetTime string `json:"resetTime"`
	Window    string `json:"window"`
	Disabled  bool   `json:"disabled"`
}

func (b quotaBucket) remainingFraction() *float64 {
	if b.RemainingFraction != nil {
		return b.RemainingFraction
	}
	if b.Remaining != nil {
		return b.Remaining.RemainingFraction
	}
	return nil
}

func parseQuotaSummary(body []byte, now time.Time) ([]provider.UsageWindow, error) {
	var summary quotaSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, fmt.Errorf("decode Antigravity quota summary: %w", err)
	}
	if len(summary.Groups) == 0 {
		return nil, fmt.Errorf("Antigravity quota summary contained no groups")
	}

	byID := make(map[string]provider.UsageWindow)
	order := make([]string, 0, len(summary.Groups))
	for _, group := range summary.Groups {
		for _, bucket := range group.Buckets {
			if bucket.Disabled {
				continue
			}
			id := strings.TrimSpace(bucket.BucketID)
			if id == "" {
				return nil, fmt.Errorf("Antigravity quota bucket is missing an id")
			}
			remaining := bucket.remainingFraction()
			if remaining == nil || math.IsNaN(*remaining) || math.IsInf(*remaining, 0) || *remaining < 0 || *remaining > 1 {
				return nil, fmt.Errorf("Antigravity quota bucket %q has invalid remaining usage", id)
			}
			resetAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(bucket.ResetTime))
			if err != nil || !resetAt.After(now) {
				return nil, fmt.Errorf("Antigravity quota bucket %q has an invalid reset time", id)
			}
			if bucket.Window != "" && !strings.EqualFold(strings.TrimSpace(bucket.Window), "weekly") {
				return nil, fmt.Errorf("Antigravity quota bucket %q has unsupported window %q", id, bucket.Window)
			}

			label := poolLabel(id, group.DisplayName)
			window := provider.UsageWindow{
				Name:        "7d " + label,
				DisplayName: "7 days (" + label + ")",
				Utilization: (1 - *remaining) * 100,
				ResetsAt:    resetAt,
			}
			if prior, exists := byID[id]; exists {
				if prior.Name != window.Name || prior.Utilization != window.Utilization || !prior.ResetsAt.Equal(window.ResetsAt) {
					return nil, fmt.Errorf("Antigravity quota summary contains conflicting duplicate bucket %q", id)
				}
				continue
			}
			byID[id] = window
			order = append(order, id)
		}
	}
	if len(byID) == 0 {
		return nil, fmt.Errorf("Antigravity quota summary contained no usable buckets")
	}

	sort.SliceStable(order, func(i, j int) bool {
		return poolOrder(order[i]) < poolOrder(order[j])
	})
	windows := make([]provider.UsageWindow, 0, len(order))
	for _, id := range order {
		windows = append(windows, byID[id])
	}
	return windows, nil
}

func poolLabel(bucketID, groupName string) string {
	switch strings.ToLower(strings.TrimSpace(bucketID)) {
	case "gemini-weekly":
		return "Gemini"
	case "3p-weekly":
		return "Claude + GPT"
	}
	label := strings.TrimSpace(groupName)
	label = strings.TrimSuffix(label, " models")
	label = strings.TrimSuffix(label, " Models")
	if label == "" {
		return "Models"
	}
	return label
}

func poolOrder(bucketID string) int {
	switch strings.ToLower(strings.TrimSpace(bucketID)) {
	case "gemini-weekly":
		return 0
	case "3p-weekly":
		return 1
	default:
		return 2
	}
}

func expiredUsage(now time.Time) *provider.UsageData {
	return &provider.UsageData{
		Provider:              "antigravity",
		FetchedAt:             now,
		IsExpired:             true,
		Error:                 "Antigravity login expired; run `agy` to sign in",
		InvalidatesPriorUsage: true,
	}
}

// Register registers the Antigravity provider with the registry.
func Register(registry *provider.Registry, _ *config.Config) error {
	return registry.Register(New())
}
