// Package antigravity implements quota monitoring for the Antigravity CLI.
package antigravity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	defaultBaseURL     = "https://daily-cloudcode-pa.googleapis.com"
	loadCodeAssistPath = "/v1internal:loadCodeAssist"
	quotaSummaryPath   = "/v1internal:retrieveUserQuotaSummary"
	defaultTokenURL    = "https://oauth2.googleapis.com/token"
	requestTimeout     = 12 * time.Second
	maxResponseBytes   = 1 << 20
	oauthSecretLength  = 35
)

type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Provider reads native CLI quota reports or an explicitly selected token's
// remote quota summary. Pool membership and cadence come from the provider.
type Provider struct {
	baseURL                    string
	tokenURL                   string
	client                     httpClient
	homeDir                    func() (string, error)
	lookPath                   func(string) (string, error)
	readFile                   func(string) ([]byte, error)
	readCLIUsage               func(context.Context) (*provider.UsageData, error)
	now                        func() time.Time
	tokenMu                    sync.Mutex
	memoryToken                storedToken
	sourceID                   string
	sourceLabel                string
	tokenPath                  string
	enrolledSource             bool
	sessionEnvironmentResolver provider.SessionEnvironmentResolver
}

func (p *Provider) SetSessionEnvironmentResolver(resolver provider.SessionEnvironmentResolver) {
	p.sessionEnvironmentResolver = resolver
}

// New creates an Antigravity provider.
func New() *Provider {
	p := &Provider{
		baseURL:  defaultBaseURL,
		tokenURL: defaultTokenURL,
		client:   &http.Client{Timeout: requestTimeout},
		homeDir:  os.UserHomeDir,
		lookPath: exec.LookPath,
		readFile: os.ReadFile,
		now:      time.Now,
	}
	p.readCLIUsage = p.fetchCLIUsage
	return p
}

func (p *Provider) Name() string        { return "antigravity" }
func (p *Provider) DisplayName() string { return "Antigravity" }
func (p *Provider) Description() string {
	return "Google Antigravity quota groups (via agy login)"
}
func (p *Provider) DashboardURL() string { return "https://antigravity.google" }
func (p *Provider) SafeForAutoPolling() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) IsConfigured() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if p.tokenPath == "" {
		if _, err := p.executablePath("agy"); err != nil {
			return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "Antigravity CLI is not installed"}
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
		if p.tokenPath == "" {
			if _, cliErr := p.executablePath("agy"); cliErr != nil {
				return provider.SetupStatus{
					State:  provider.SetupUnavailable,
					Detail: "Antigravity CLI is not installed",
				}
			}
		}
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "run `agy` to sign in",
		}
	}
	return provider.SetupStatus{State: provider.SetupReady, Detail: "Antigravity CLI login found"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	// The native CLI reports all of its pools. The legacy remote summary can
	// omit Gemini entirely. Never attribute an ambient CLI login to an explicit
	// token-file source; those sources retain their account-scoped API path.
	if p.readCLIUsage != nil && p.tokenPath == "" {
		data, err := p.readCLIUsage(ctx)
		return p.withSource(data), err
	}
	token, err := p.validAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	project, err := p.loadProject(ctx, token)
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return p.withSource(expiredUsage(p.now())), nil
		}
		return nil, err
	}
	body, err := p.postJSON(ctx, quotaSummaryPath, token, map[string]string{"project": project})
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return p.withSource(expiredUsage(p.now())), nil
		}
		return nil, err
	}

	windows, err := parseQuotaSummary(body, p.now())
	if err != nil {
		return nil, err
	}
	return p.withSource(&provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: p.now(),
		Windows:   windows,
	}), nil
}

type sourceCapability struct{}

func (*Provider) SourceKinds() []provider.SourceKind { return (sourceCapability{}).SourceKinds() }
func (*Provider) DefaultSource() (config.SourceConfig, bool) {
	return (sourceCapability{}).DefaultSource()
}
func (*Provider) ValidateSource(source config.SourceConfig) error {
	return (sourceCapability{}).ValidateSource(source)
}
func (*Provider) NewSource(_ config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	return (sourceCapability{}).NewSource(config.ProviderConfig{}, source)
}

func (sourceCapability) SourceKinds() []provider.SourceKind {
	return []provider.SourceKind{
		{Kind: "native", Summary: "Provider's legacy/default credential route"},
		{Kind: "token-file", Summary: "Antigravity OAuth token file; absolute path required", RefUsage: "/absolute/path/to/antigravity-oauth-token", RefRequired: true, RefIsPath: true},
	}
}

func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

func (sourceCapability) ValidateSource(source config.SourceConfig) error {
	kind := strings.TrimSpace(source.Credential.Kind)
	switch kind {
	case "native":
		if strings.TrimSpace(source.ID) != "default" || strings.TrimSpace(source.Credential.Ref) != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "antigravity", source.ID)
		}
	case "token-file":
		ref := strings.TrimSpace(source.Credential.Ref)
		if ref == "" {
			return fmt.Errorf("provider %q source %q has empty token file", "antigravity", source.ID)
		}
		if !filepath.IsAbs(ref) {
			return fmt.Errorf("provider %q source %q has relative token file", "antigravity", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "antigravity", source.ID, kind)
	}
	return nil
}

func (sourceCapability) NewSource(_ config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(source); err != nil {
		return nil, err
	}
	p := New()
	p.sourceID = strings.TrimSpace(source.ID)
	p.sourceLabel = strings.TrimSpace(source.Label)
	p.enrolledSource = true
	if strings.TrimSpace(source.Credential.Kind) == "token-file" {
		p.tokenPath = strings.TrimSpace(source.Credential.Ref)
	}
	return p, nil
}

func NewSource(source config.SourceConfig) *Provider {
	p, _ := (sourceCapability{}).NewSource(config.ProviderConfig{}, source)
	return p.(*Provider)
}

// CredentialRoute names the token file this source reads, so a repointed
// source keeps its own rate-limit backoff across token refreshes.
func (p *Provider) CredentialRoute() string {
	if path := strings.TrimSpace(p.tokenPath); path != "" {
		return provider.CredentialRoute("token-file", path)
	}
	return "native"
}

func (p *Provider) SourceID() string {
	if strings.TrimSpace(p.sourceID) == "" {
		return "default"
	}
	return strings.TrimSpace(p.sourceID)
}

func (p *Provider) SourceLabel() string    { return p.sourceLabel }
func (p *Provider) IsEnrolledSource() bool { return p.enrolledSource }

func (p *Provider) SourceRevision() string {
	if strings.TrimSpace(p.tokenPath) == "" {
		return ""
	}
	canonical, err := filepath.Abs(p.tokenPath)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	info, err := os.Stat(p.tokenPath)
	if err != nil {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical+"\x00unavailable")))
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%o", canonical, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))))
}

func (p *Provider) withSource(data *provider.UsageData) *provider.UsageData {
	if data == nil {
		return nil
	}
	data.SourceID = p.SourceID()
	data.SourceLabel = p.SourceLabel()
	return data
}

type storedToken struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

type tokenFile struct {
	Token struct {
		AccessToken  string    `json:"access_token"`
		RefreshToken string    `json:"refresh_token"`
		Expiry       time.Time `json:"expiry"`
	} `json:"token"`
}

func (p *Provider) validAccessToken(ctx context.Context) (string, error) {
	token, err := p.readToken()
	if err == nil && token.AccessToken != "" && token.Expiry.After(p.now().Add(time.Minute)) {
		return token.AccessToken, nil
	}

	if errors.Is(err, errTokenPermissions) {
		return "", fmt.Errorf("Antigravity login file must be accessible only to its owner")
	}
	if errors.Is(err, errTokenMalformed) {
		return "", fmt.Errorf("Antigravity login is unreadable; run `agy` to sign in again")
	}
	if err != nil {
		return "", fmt.Errorf("Antigravity login is not available to Clawmeter; run `agy` to sign in")
	}
	if cached := p.validMemoryToken(); cached != "" {
		return cached, nil
	}
	if token.RefreshToken == "" {
		return "", fmt.Errorf("Antigravity login expired; run `agy` to sign in")
	}
	return p.refreshAccessToken(ctx, token.RefreshToken)
}

var (
	errTokenPermissions = errors.New("Antigravity login permissions are too broad")
	errTokenMalformed   = errors.New("Antigravity login is malformed")
)

func (p *Provider) readToken() (storedToken, error) {
	path := strings.TrimSpace(p.tokenPath)
	if path == "" {
		home, err := p.homeDir()
		if err != nil {
			return storedToken{}, fmt.Errorf("locate Antigravity login: %w", err)
		}
		path = filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	}
	info, err := os.Stat(path)
	if err != nil {
		return storedToken{}, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return storedToken{}, errTokenPermissions
	}
	data, err := p.readFile(path)
	if err != nil {
		return storedToken{}, err
	}
	var stored tokenFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return storedToken{}, fmt.Errorf("%w: %v", errTokenMalformed, err)
	}
	return storedToken{
		AccessToken:  strings.TrimSpace(stored.Token.AccessToken),
		RefreshToken: strings.TrimSpace(stored.Token.RefreshToken),
		Expiry:       stored.Token.Expiry,
	}, nil
}

func (p *Provider) validMemoryToken() string {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()
	if p.memoryToken.AccessToken != "" && p.memoryToken.Expiry.After(p.now().Add(time.Minute)) {
		return p.memoryToken.AccessToken
	}
	return ""
}

type oauthClient struct {
	ID     string
	Secret string
}

func (p *Provider) refreshAccessToken(ctx context.Context, refreshToken string) (string, error) {
	clients, err := p.resolveOAuthClients()
	if err != nil {
		return "", fmt.Errorf("Antigravity login expired and its OAuth client could not be read; update `agy` or sign in again")
	}
	for _, client := range clients {
		token, retry, err := p.refreshAccessTokenWithClient(ctx, refreshToken, client)
		if err == nil {
			return token, nil
		}
		if !retry {
			return "", err
		}
	}
	return "", fmt.Errorf("Antigravity login expired; run `agy` to sign in")
}

func (p *Provider) refreshAccessTokenWithClient(
	ctx context.Context,
	refreshToken string,
	client oauthClient,
) (string, bool, error) {
	form := url.Values{
		"client_id":     {client.ID},
		"client_secret": {client.Secret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Clawmeter Antigravity quota monitor")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("refresh Antigravity login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("read Antigravity login refresh: %w", err)
	}
	if len(body) > maxResponseBytes {
		return "", false, fmt.Errorf("Antigravity login refresh exceeded size limit")
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
			return "", true, fmt.Errorf("Antigravity login refresh rejected")
		}
		return "", false, fmt.Errorf("Antigravity OAuth service returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil ||
		strings.TrimSpace(result.AccessToken) == "" ||
		result.ExpiresIn <= 60 {
		return "", false, fmt.Errorf("Antigravity returned an invalid login refresh")
	}
	token := storedToken{
		AccessToken: strings.TrimSpace(result.AccessToken),
		Expiry:      p.now().Add(time.Duration(result.ExpiresIn) * time.Second),
	}
	p.tokenMu.Lock()
	p.memoryToken = token
	p.tokenMu.Unlock()
	return token.AccessToken, false, nil
}

func (p *Provider) resolveOAuthClients() ([]oauthClient, error) {
	if p.sessionEnvironmentResolver != nil {
		values := p.sessionEnvironmentResolver.ResolveSessionEnvironment(provider.SessionEnvironmentRequest{
			EnvNames: []string{"ANTIGRAVITY_OAUTH_CLIENT_ID", "ANTIGRAVITY_OAUTH_CLIENT_SECRET"}, AllowSessionEnvironmentFallback: true,
		})
		if id := strings.TrimSpace(values["ANTIGRAVITY_OAUTH_CLIENT_ID"]); id != "" {
			if secret := strings.TrimSpace(values["ANTIGRAVITY_OAUTH_CLIENT_SECRET"]); secret != "" {
				return []oauthClient{{ID: id, Secret: secret}}, nil
			}
		}
	} else if id := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_ID")); id != "" {
		if secret := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET")); secret != "" {
			return []oauthClient{{ID: id, Secret: secret}}, nil
		}
	}
	path, err := p.executablePath("agy")
	if err != nil {
		return nil, err
	}
	data, err := p.readFile(path)
	if err != nil {
		return nil, err
	}
	return parseOAuthClients(data)
}

func (p *Provider) executablePath(name string) (string, error) {
	path, err := p.lookPath(name)
	if err == nil {
		return path, nil
	}
	if resolver, ok := p.sessionEnvironmentResolver.(provider.SessionExecutableResolver); ok {
		return resolver.ResolveSessionExecutable(name)
	}
	return "", err
}

func parseOAuthClients(data []byte) ([]oauthClient, error) {
	ids := oauthClientIDs(data)
	secrets := oauthClientSecrets(data)
	if len(ids) == 0 || len(secrets) == 0 {
		return nil, fmt.Errorf("OAuth client metadata not found")
	}
	if len(ids) > 8 || len(secrets) > 8 {
		return nil, fmt.Errorf("too many OAuth client candidates")
	}
	clients := make([]oauthClient, 0, len(ids)*len(secrets))
	for _, id := range ids {
		for _, secret := range secrets {
			clients = append(clients, oauthClient{ID: id, Secret: secret})
		}
	}
	return clients, nil
}

func oauthClientIDs(data []byte) []string {
	pattern := regexp.MustCompile(`[0-9]+-[A-Za-z0-9_-]+\.apps\.googleusercontent\.com`)
	var values []string
	for _, match := range pattern.FindAll(data, -1) {
		values = appendUnique(values, string(match))
	}
	return values
}

func oauthClientSecrets(data []byte) []string {
	const prefix = "GOCSPX-"
	var values []string
	for offset := 0; offset < len(data); {
		index := bytes.Index(data[offset:], []byte(prefix))
		if index < 0 {
			break
		}
		start := offset + index
		end := start + oauthSecretLength
		if end <= len(data) {
			candidate := data[start:end]
			valid := true
			for _, value := range candidate[len(prefix):] {
				if !isOAuthByte(value) {
					valid = false
					break
				}
			}
			if valid {
				values = appendUnique(values, string(candidate))
			}
		}
		offset = start + len(prefix)
	}
	return values
}

func isOAuthByte(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value == '-' || value == '_'
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
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
	if err := provider.RateLimitFromResponse(resp); err != nil {
		return nil, err
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
	Description string        `json:"description"`
	Buckets     []quotaBucket `json:"buckets"`
}

type quotaBucket struct {
	BucketID          string   `json:"bucketId"`
	DisplayName       string   `json:"displayName"`
	Description       string   `json:"description"`
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
		cadences := make(map[time.Duration]int)
		for _, bucket := range group.Buckets {
			if !bucket.Disabled {
				cadences[quotaWindowLength(bucket.Window)]++
			}
		}
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
			var resetAt time.Time
			if raw := strings.TrimSpace(bucket.ResetTime); raw != "" {
				var err error
				resetAt, err = time.Parse(time.RFC3339Nano, raw)
				if err != nil {
					return nil, fmt.Errorf("Antigravity quota bucket %q has an invalid reset time", id)
				}
			}

			label := strings.TrimSpace(group.DisplayName)
			label = strings.TrimSuffix(strings.TrimSuffix(label, " models"), " Models")
			if label == "" {
				label = id
			}
			length := quotaWindowLength(bucket.Window)
			if cadences[length] > 1 {
				name := strings.TrimSpace(bucket.DisplayName)
				if name == "" {
					name = id
				}
				label += " · " + name
			}
			display := label
			if length > 0 {
				display = quotaDurationLabel(length) + " (" + label + ")"
			}
			window := provider.UsageWindow{
				Name:          quotaWindowName(id),
				Length:        length,
				LengthUnknown: length == 0,
				DisplayName:   display,
				Scope:         strings.TrimSpace(group.Description),
				Description:   strings.TrimSpace(bucket.Description),
				Utilization:   (1 - *remaining) * 100,
				ResetsAt:      resetAt,
			}
			if prior, exists := byID[id]; exists {
				if prior != window {
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

	windows := make([]provider.UsageWindow, 0, len(order))
	for _, id := range order {
		windows = append(windows, byID[id])
	}
	return windows, nil
}

// These two aliases preserve existing cache and pin identities. They do not
// select supported models, supply labels, infer cadence, or filter new pools.
func quotaWindowName(bucketID string) string {
	switch bucketID {
	case "gemini-weekly":
		return "7d Gemini"
	case "3p-weekly":
		return "7d Claude + GPT"
	}
	return bucketID
}

func quotaWindowLength(cadence string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(cadence)) {
	case "weekly":
		return 7 * 24 * time.Hour
	case "daily":
		return 24 * time.Hour
	case "hourly":
		return time.Hour
	}
	// Explicit numeric durations remain useful when the provider adds a new
	// cadence. Unknown and calendar-based cadences do not imply a fixed length.
	if d, err := time.ParseDuration(strings.TrimSpace(cadence)); err == nil && d > 0 {
		return d
	}
	return 0
}

func quotaDurationLabel(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		if d == 24*time.Hour {
			return "1 day"
		}
		return fmt.Sprintf("%d days", d/(24*time.Hour))
	}
	if d%time.Hour == 0 {
		if d == time.Hour {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", d/time.Hour)
	}
	return d.String()
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
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("antigravity")
	return provider.RegisterConfigured(registry, providerCfg, New())
}
