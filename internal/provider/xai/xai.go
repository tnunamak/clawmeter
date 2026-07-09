// Package xai implements the Provider interface for xAI/Grok usage.
package xai

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	managementBaseURL = "https://management-api.x.ai"
	grokBillingURL    = "https://grok.com/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig"
	timeout           = 10 * time.Second
)

// Provider implements the provider.Provider interface for xAI.
type Provider struct {
	cfg            config.ProviderConfig
	baseURL        string
	grokBillingURL string
	client         *http.Client
}

// New creates a new xAI provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg:            cfg,
		baseURL:        managementBaseURL,
		grokBillingURL: grokBillingURL,
		client:         &http.Client{Timeout: timeout},
	}
}

func (p *Provider) Name() string         { return "xai" }
func (p *Provider) DisplayName() string  { return "Grok" }
func (p *Provider) Description() string  { return "Grok weekly usage pool or xAI API prepaid credits" }
func (p *Provider) DashboardURL() string { return "https://grok.com/?_s=usage" }
func (p *Provider) AutoPollByDefault() bool {
	_, err := p.grokCredentials()
	return err == nil
}

func (p *Provider) IsConfigured() bool {
	if _, err := p.managementKey(); err == nil {
		return true
	}
	_, err := p.grokCredentials()
	return err == nil
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if _, err := p.managementKey(); err == nil {
		if _, err := p.configuredTeamID(); err == nil {
			return provider.SetupStatus{State: provider.SetupReady, Detail: "management key and team id found"}
		}
		return provider.SetupStatus{State: provider.SetupReady, Detail: "management key found; team id will be discovered"}
	}
	if _, err := p.grokCredentials(); err == nil {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "grok login credentials found"}
	} else if errors.Is(err, errGrokCredentialsExpired) {
		return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "grok auth expired — run `grok login`"}
	} else if errors.Is(err, errGrokCredentialsMalformed) {
		return provider.SetupStatus{State: provider.SetupNeedsAuth, Detail: "grok auth is unreadable — run `grok login`"}
	}
	if _, err := exec.LookPath("grok"); err == nil {
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "run `grok login` or set XAI_MANAGEMENT_API_KEY",
		}
	}
	return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "no XAI_MANAGEMENT_API_KEY or grok login credentials"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	key, err := p.managementKey()
	if err != nil {
		return p.fetchGrokBuildUsage(ctx)
	}
	return p.fetchPrepaidCredits(ctx, key)
}

func (p *Provider) fetchPrepaidCredits(ctx context.Context, key string) (*provider.UsageData, error) {
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

func (p *Provider) fetchGrokBuildUsage(ctx context.Context) (*provider.UsageData, error) {
	credentials, err := p.grokCredentials()
	if err != nil {
		if errors.Is(err, errGrokCredentialsExpired) {
			return &provider.UsageData{
				Provider:  p.Name(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "grok auth expired — run `grok login`",
			}, nil
		}
		return nil, fmt.Errorf("credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.grokBillingURL, bytes.NewReader([]byte{0, 0, 0, 0, 0}))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	req.Header.Set("Origin", "https://grok.com")
	req.Header.Set("Referer", "https://grok.com/?_s=usage")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	req.Header.Set("x-user-agent", "connect-es/2.1.1")
	req.Header.Set("User-Agent", "Clawmeter")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &provider.UsageData{
			Provider:  p.Name(),
			FetchedAt: time.Now(),
			IsExpired: true,
			Error:     "grok auth rejected — run `grok login`",
		}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read Grok billing response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Grok billing returned HTTP %d", resp.StatusCode)
	}
	if err := validateGRPCStatus(grpcStatusFields(resp.Header), body); err != nil {
		if isGrokAuthError(err) {
			return &provider.UsageData{
				Provider:  p.Name(),
				FetchedAt: time.Now(),
				IsExpired: true,
				Error:     "grok auth rejected — run `grok login`",
			}, nil
		}
		return nil, err
	}

	snapshot, err := parseGrokBilling(body, time.Now())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	windowName, windowDisplayName := grokSubscriptionWindowLabels(snapshot.ResetsAt, now)
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: now,
		Windows: []provider.UsageWindow{{
			Name:        windowName,
			DisplayName: windowDisplayName,
			Utilization: snapshot.UsedPercent,
			ResetsAt:    snapshot.ResetsAt,
		}},
	}
	return data, nil
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

var (
	errGrokCredentialsNotFound  = errors.New("grok auth not found")
	errGrokCredentialsMalformed = errors.New("grok auth malformed")
	errGrokCredentialsExpired   = errors.New("grok auth expired")
)

type grokCredentials struct {
	AccessToken string
	ExpiresAt   time.Time
}

func (p *Provider) grokCredentials() (*grokCredentials, error) {
	path, err := grokAuthPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errGrokCredentialsNotFound
		}
		return nil, fmt.Errorf("%w: %v", errGrokCredentialsMalformed, err)
	}

	var root map[string]struct {
		Key       string `json:"key"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", errGrokCredentialsMalformed, err)
	}

	var fallback *grokCredentials
	for scope, entry := range root {
		if strings.TrimSpace(entry.Key) == "" {
			continue
		}
		credentials, err := parseGrokCredentialEntry(entry.Key, entry.ExpiresAt)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(scope, "https://auth.x.ai::") {
			return credentials, nil
		}
		if scope == "https://accounts.x.ai/sign-in" || strings.Contains(scope, "/sign-in") {
			fallback = credentials
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errGrokCredentialsNotFound
}

func grokAuthPath() (string, error) {
	if home := strings.TrimSpace(os.Getenv("GROK_HOME")); home != "" {
		return filepath.Join(expandTilde(home), "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".grok", "auth.json"), nil
}

func expandTilde(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func parseGrokCredentialEntry(token, expiresAt string) (*grokCredentials, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errGrokCredentialsNotFound
	}
	credentials := &grokCredentials{AccessToken: token}
	if strings.TrimSpace(expiresAt) != "" {
		t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(expiresAt))
		if err != nil {
			return nil, fmt.Errorf("%w: invalid expires_at", errGrokCredentialsMalformed)
		}
		credentials.ExpiresAt = t
		if !t.After(time.Now()) {
			return nil, errGrokCredentialsExpired
		}
	}
	return credentials, nil
}

type grokBillingSnapshot struct {
	UsedPercent float64
	ResetsAt    time.Time
}

type grokGRPCError struct {
	status  int
	message string
}

func (e grokGRPCError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("Grok billing RPC failed with status %d", e.status)
	}
	return fmt.Sprintf("Grok billing RPC failed with status %d: %s", e.status, e.message)
}

func isGrokAuthError(err error) bool {
	var grpcErr grokGRPCError
	if errors.As(err, &grpcErr) {
		if grpcErr.status == 16 {
			return true
		}
		if grpcErr.status == 7 {
			msg := strings.ToLower(grpcErr.message)
			return strings.Contains(msg, "bad-credentials") ||
				strings.Contains(msg, "unauthenticated") ||
				strings.Contains(msg, "access token") ||
				strings.Contains(msg, "oauth2")
		}
	}
	return false
}

func grpcStatusFields(headers http.Header) map[string]string {
	fields := make(map[string]string)
	for key, values := range headers {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasPrefix(normalized, "grpc-") || len(values) == 0 {
			continue
		}
		value := strings.TrimSpace(values[0])
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		fields[normalized] = value
	}
	return fields
}

func validateGRPCStatus(headerFields map[string]string, body []byte) error {
	if err := validateGRPCStatusFields(headerFields); err != nil {
		return err
	}
	return validateGRPCStatusFields(grpcWebTrailerFields(body))
}

func validateGRPCStatusFields(fields map[string]string) error {
	raw, ok := fields["grpc-status"]
	if !ok || raw == "" {
		return nil
	}
	status, err := strconv.Atoi(raw)
	if err != nil || status == 0 {
		return nil
	}
	return grokGRPCError{status: status, message: fields["grpc-message"]}
}

func grpcWebTrailerFields(data []byte) map[string]string {
	fields := make(map[string]string)
	for len(data) >= 5 {
		flags := data[0]
		length := int(binary.BigEndian.Uint32(data[1:5]))
		if length < 0 || len(data) < 5+length {
			break
		}
		frame := data[5 : 5+length]
		if flags&0x80 != 0 {
			for _, line := range strings.Split(string(frame), "\n") {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				value = strings.TrimSpace(value)
				if decoded, err := url.QueryUnescape(value); err == nil {
					value = decoded
				}
				fields[strings.ToLower(strings.TrimSpace(key))] = value
			}
		}
		data = data[5+length:]
	}
	return fields
}

type protobufScan struct {
	fixed32 []fixed32Field
	varints []varintField
	order   int
}

type fixed32Field struct {
	path  []uint64
	value float32
	order int
}

type varintField struct {
	path  []uint64
	value uint64
}

func parseGrokBilling(data []byte, now time.Time) (*grokBillingSnapshot, error) {
	payloads := grpcWebDataFrames(data)
	if len(payloads) == 0 && looksLikeProtobuf(data) {
		payloads = [][]byte{data}
	}
	if len(payloads) == 0 {
		return nil, fmt.Errorf("Grok billing returned no protobuf payload")
	}

	scan := &protobufScan{}
	for _, payload := range payloads {
		scan.scan(payload, nil, 0)
	}

	percent, hasPercent := scan.grokUsedPercent()
	reset, hasReset := scan.grokReset(now)
	hasUsagePeriod := scan.hasGrokUsagePeriod()
	noUsageYet := !hasPercent && len(scan.fixed32) == 0 && hasReset && hasUsagePeriod
	if noUsageYet {
		percent = 0
		hasPercent = true
	}
	if !hasPercent {
		return nil, fmt.Errorf("could not parse Grok billing usage")
	}
	if !hasReset {
		return nil, fmt.Errorf("could not parse Grok billing reset time")
	}
	return &grokBillingSnapshot{UsedPercent: percent, ResetsAt: reset}, nil
}

func grpcWebDataFrames(data []byte) [][]byte {
	var frames [][]byte
	for len(data) >= 5 {
		flags := data[0]
		length := int(binary.BigEndian.Uint32(data[1:5]))
		if length < 0 || len(data) < 5+length {
			return nil
		}
		frame := data[5 : 5+length]
		if flags&0x80 == 0 {
			frames = append(frames, frame)
		}
		data = data[5+length:]
	}
	if len(data) != 0 {
		return nil
	}
	return frames
}

func looksLikeProtobuf(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	fieldNumber := data[0] >> 3
	wireType := data[0] & 0x07
	return fieldNumber > 0 && (wireType == 0 || wireType == 1 || wireType == 2 || wireType == 5)
}

func (s *protobufScan) scan(data []byte, path []uint64, depth int) {
	for i := 0; i < len(data); {
		fieldStart := i
		key, ok := readVarint(data, &i)
		if !ok || key == 0 {
			i = fieldStart + 1
			continue
		}
		fieldNumber := key >> 3
		wireType := key & 0x07
		fieldPath := append(append([]uint64(nil), path...), fieldNumber)

		switch wireType {
		case 0:
			if value, ok := readVarint(data, &i); ok {
				s.varints = append(s.varints, varintField{path: fieldPath, value: value})
			} else {
				i = fieldStart + 1
			}
		case 1:
			if i+8 > len(data) {
				return
			}
			i += 8
		case 2:
			length, ok := readVarint(data, &i)
			if !ok || length > uint64(len(data)-i) {
				i = fieldStart + 1
				continue
			}
			start := i
			end := i + int(length)
			if depth < 4 {
				s.scan(data[start:end], fieldPath, depth+1)
			}
			i = end
		case 5:
			if i+4 > len(data) {
				return
			}
			bits := binary.LittleEndian.Uint32(data[i : i+4])
			s.fixed32 = append(s.fixed32, fixed32Field{
				path:  fieldPath,
				value: math.Float32frombits(bits),
				order: s.order,
			})
			s.order++
			i += 4
		default:
			i = fieldStart + 1
		}
	}
}

func readVarint(data []byte, index *int) (uint64, bool) {
	var value uint64
	var shift uint
	for *index < len(data) && shift < 64 {
		b := data[*index]
		*index = *index + 1
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, true
		}
		shift += 7
	}
	return 0, false
}

func (s *protobufScan) grokUsedPercent() (float64, bool) {
	var best *fixed32Field
	for i := range s.fixed32 {
		field := &s.fixed32[i]
		if len(field.path) == 0 || field.path[len(field.path)-1] != 1 {
			continue
		}
		value := float64(field.value)
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			continue
		}
		if best == nil ||
			len(field.path) < len(best.path) ||
			(len(field.path) == len(best.path) && field.order < best.order) {
			best = field
		}
	}
	if best == nil {
		return 0, false
	}
	return float64(best.value), true
}

func (s *protobufScan) grokReset(now time.Time) (time.Time, bool) {
	var preferred []time.Time
	var fallback []time.Time
	for _, field := range s.varints {
		if field.value < 1_700_000_000 || field.value > 2_100_000_000 {
			continue
		}
		t := time.Unix(int64(field.value), 0).UTC()
		if !t.After(now) {
			continue
		}
		if samePath(field.path, []uint64{1, 5, 1}) {
			preferred = append(preferred, t)
		}
		fallback = append(fallback, t)
	}
	if len(preferred) > 0 {
		return earliestTime(preferred), true
	}
	if len(fallback) > 0 {
		return earliestTime(fallback), true
	}
	return time.Time{}, false
}

func (s *protobufScan) hasGrokUsagePeriod() bool {
	for _, field := range s.varints {
		if pathHasPrefix(field.path, []uint64{1, 6}) {
			return true
		}
		if samePath(field.path, []uint64{1, 8, 1}) && (field.value == 1 || field.value == 2) {
			return true
		}
	}
	return false
}

func samePath(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pathHasPrefix(path, prefix []uint64) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func earliestTime(times []time.Time) time.Time {
	earliest := times[0]
	for _, t := range times[1:] {
		if t.Before(earliest) {
			earliest = t
		}
	}
	return earliest
}

func grokSubscriptionWindowLabels(resetsAt, now time.Time) (string, string) {
	if resetsAt.IsZero() {
		return "usage", "Usage"
	}
	days := int(math.Round(resetsAt.Sub(now).Hours() / 24))
	if days >= 4 && days <= 12 {
		return "7d", "Weekly usage pool"
	}
	if days >= 20 && days <= 45 {
		return "monthly", "Monthly usage"
	}
	return "usage", "Usage"
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
