// Package openai implements the Provider interface for Codex via JSON-RPC subprocess.
package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	maxFetchAttempts = 2
	retryDelay       = 150 * time.Millisecond
	maxStderrBytes   = 4096
)

var (
	errNoResponse    = errors.New("no response received")
	appServerTimeout = 15 * time.Second
	appServerBudget  = 18 * time.Second
)

// Provider implements the provider.Provider interface for Codex.
type Provider struct {
	cfg            config.ProviderConfig
	sourceID       string
	sourceLabel    string
	codexHome      string
	explicitSource bool
	enrolledSource bool
}

// New creates a new Codex provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg:         cfg,
		sourceID:    "default",
		sourceLabel: "Default",
	}
}

func NewNativeSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	p, _ := (sourceCapability{}).NewSource(cfg, source)
	return p.(*Provider)
}

func NewSource(cfg config.ProviderConfig, source config.SourceConfig) (*Provider, error) {
	p, err := (sourceCapability{}).NewSource(cfg, source)
	if err != nil {
		return nil, err
	}
	return p.(*Provider), nil
}

func (p *Provider) Name() string         { return "openai" } // stable config key
func (p *Provider) DisplayName() string  { return "Codex" }
func (p *Provider) Description() string  { return "Codex quota (via local Codex auth)" }
func (p *Provider) DashboardURL() string { return "https://platform.openai.com/usage" }
func (p *Provider) SourceID() string {
	if p.sourceID == "" {
		return "default"
	}
	return p.sourceID
}
func (p *Provider) SourceLabel() string    { return p.sourceLabel }
func (p *Provider) IsEnrolledSource() bool { return p.enrolledSource }
func (p *Provider) SourceRevision() string { return p.sourceRevision() }

func (p *Provider) subprocessEnv() []string {
	env := replaceEnv(os.Environ(), "NO_COLOR", "1", runtime.GOOS == "windows")
	if p.explicitSource {
		env = replaceEnv(env, "CODEX_HOME", p.codexHome, runtime.GOOS == "windows")
	}
	return env
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
		{Kind: "codex-home", Summary: "Codex home directory; absolute path required", RefUsage: "/absolute/path", RefRequired: true, RefIsPath: true},
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
			return fmt.Errorf("provider %q source %q cannot use native credentials", "openai", source.ID)
		}
	case "codex-home":
		ref := strings.TrimSpace(source.Credential.Ref)
		if ref == "" {
			return fmt.Errorf("provider %q source %q has empty Codex home", "openai", source.ID)
		}
		if !filepath.IsAbs(ref) {
			return fmt.Errorf("provider %q source %q has relative Codex home", "openai", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "openai", source.ID, kind)
	}
	return nil
}

func (sourceCapability) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(source); err != nil {
		return nil, err
	}
	label := strings.TrimSpace(source.Label)
	if label == "" && strings.TrimSpace(source.ID) == "default" {
		label = "Default"
	}
	p := &Provider{cfg: cfg, sourceID: strings.TrimSpace(source.ID), sourceLabel: label, enrolledSource: true}
	if source.Credential.Kind == "codex-home" {
		p.codexHome = filepath.Clean(strings.TrimSpace(source.Credential.Ref))
		p.explicitSource = true
	}
	return p, nil
}

// FetchUsage retrieves rate limit data by launching codex as a JSON-RPC subprocess.
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	codexPath, err := codexExecutablePath()
	if err != nil {
		return p.fetchUsageWithoutCLI(ctx)
	}

	// Keep the app-server phase below the tray's 30-second refresh deadline so
	// retryable transport failures cannot consume the direct fallback's budget.
	appCtx, cancelAppServer := context.WithTimeout(ctx, appServerBudget)
	defer cancelAppServer()

	var lastErr error

appServerAttempts:
	for attempt := 1; attempt <= maxFetchAttempts; attempt++ {
		data, err := p.fetchUsageOnce(appCtx, codexPath)
		if err != nil {
			lastErr = err
		} else if data == nil {
			lastErr = errors.New("codex app-server returned no usage data")
		} else if provider.IsTransientFetchError(data.Error) {
			lastErr = errors.New(data.Error)
		} else {
			p.attachResetCredits(ctx, data)
			return data, nil
		}
		// A timed-out attempt has already spent the app-server budget. Retrying
		// would starve the read-only HTTP fallback under the 30-second caller
		// deadline. Quick transient failures still get one retry.
		if attempt == maxFetchAttempts || errors.Is(lastErr, context.DeadlineExceeded) || !isRetryableAppServerError(lastErr) || appCtx.Err() != nil {
			break
		}
		select {
		case <-time.After(retryDelay):
		case <-appCtx.Done():
			break appServerAttempts
		}
	}
	cancelAppServer()
	if data, directErr := p.fetchUsageWithoutCLI(ctx); directErr == nil {
		return data, nil
	}
	return nil, lastErr
}

func (p *Provider) fetchUsageWithoutCLI(ctx context.Context) (*provider.UsageData, error) {
	auth, err := readAuthFile(p.authDirectory())
	if err != nil {
		if p.explicitSource {
			return nil, fmt.Errorf("codex source unavailable")
		}
		return nil, fmt.Errorf("codex not found on PATH")
	}
	data, err := p.fetchUsageDirect(ctx, auth)
	if err != nil {
		return nil, err
	}
	p.attachResetCredits(ctx, data)
	return data, nil
}

func (p *Provider) fetchUsageOnce(ctx context.Context, codexPath string) (*provider.UsageData, error) {
	ctx, cancel := context.WithTimeout(ctx, appServerTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, codexPath, "-s", "read-only", "-a", "untrusted", "app-server")
	hideSubprocessWindow(cmd)
	cmd.Env = p.subprocessEnv()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	stderrCh := collectStderr(stderr)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}
	finished := false
	cleanup := func(kill bool) (string, error) {
		stdin.Close()
		killed := false
		if kill && cmd.Process != nil {
			if err := cmd.Process.Kill(); err == nil {
				killed = true
			}
		}
		waitErr := cmd.Wait()
		if killed {
			waitErr = nil
		}
		return <-stderrCh, waitErr
	}
	defer func() {
		if !finished {
			_, _ = cleanup(true)
		}
	}()
	fail := func(stage string, err error) (*provider.UsageData, error) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		stderrText, waitErr := cleanup(true)
		finished = true
		return nil, &appServerError{
			stage:   stage,
			err:     err,
			waitErr: waitErr,
			stderr:  stderrText,
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	// Step 1: initialize (matching CodexBar's wire format — no jsonrpc field)
	if err := writeJSON(stdin, map[string]interface{}{
		"id":     1,
		"method": "initialize",
		"params": map[string]interface{}{
			"clientInfo": map[string]interface{}{
				"name":    "clawmeter",
				"version": "1.0.0",
			},
		},
	}); err != nil {
		return fail("send initialize", err)
	}

	if _, err := readResponse(scanner); err != nil {
		return fail("read initialize response", err)
	}

	// Step 2: initialized notification
	if err := writeJSON(stdin, map[string]interface{}{
		"method": "initialized",
		"params": map[string]interface{}{},
	}); err != nil {
		return fail("send initialized", err)
	}

	// Step 3: account/read — check auth type
	if err := writeJSON(stdin, map[string]interface{}{
		"id":     2,
		"method": "account/read",
		"params": map[string]interface{}{},
	}); err != nil {
		return fail("send account/read", err)
	}

	acctData, err := readResponse(scanner)
	if err != nil {
		return fail("read account response", err)
	}

	acct, err := parseAccountResponse(acctData)
	if err != nil {
		return nil, err
	}

	// Step 4: account/rateLimits/read
	if err := writeJSON(stdin, map[string]interface{}{
		"id":     3,
		"method": "account/rateLimits/read",
		"params": map[string]interface{}{},
	}); err != nil {
		return fail("send rateLimits", err)
	}

	respData, err := readResponse(scanner)
	if err != nil {
		return fail("read rateLimits response", err)
	}

	data, err := p.parseRateLimits(respData, acct)
	stderrText, waitErr := cleanup(true)
	finished = true
	if err != nil {
		return nil, err
	}
	if waitErr != nil && data == nil {
		return nil, &appServerError{stage: "codex app-server", err: waitErr, stderr: stderrText}
	}
	return data, nil
}

// Account response types

type accountResponse struct {
	Account            *accountDetails `json:"account"`
	RequiresOpenAIAuth bool            `json:"requiresOpenaiAuth"`
}

type accountDetails struct {
	Type     string `json:"type"`     // "apiKey" or "chatgpt"
	Email    string `json:"email"`    // only for chatgpt type
	PlanType string `json:"planType"` // only for chatgpt type
}

func parseAccountResponse(data []byte) (*accountResponse, error) {
	var resp struct {
		Result *accountResponse `json:"result"`
		Error  *rpcError        `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse account response: %w", err)
	}
	if resp.Error != nil {
		return &accountResponse{}, nil
	}
	if resp.Result == nil {
		return &accountResponse{}, nil
	}
	return resp.Result, nil
}

// Rate limits parsing

func (p *Provider) parseRateLimits(data []byte, acct *accountResponse) (*provider.UsageData, error) {
	var resp struct {
		Result *rateLimitsResult `json:"result"`
		Error  *rpcError         `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if resp.Error != nil {
		msg := resp.Error.Message
		// API key users can't read rate limits — show informational message
		if acct.Account != nil && acct.Account.Type == "apiKey" {
			msg = "API key · billed per token (codex login for ChatGPT)"
		}
		return &provider.UsageData{
			Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
			FetchedAt: time.Now(),
			Error:     msg,
		}, nil
	}

	if resp.Result == nil || resp.Result.RateLimits == nil {
		return &provider.UsageData{
			Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
			FetchedAt: time.Now(),
			Error:     "no rate limit data",
		}, nil
	}

	rl := resp.Result.RateLimits
	result := &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	if validRateLimitWindow(rl.Primary) {
		primaryReset := time.Unix(rl.Primary.ResetsAt, 0)
		name, displayName := codexWindowLabels(rl.Primary.WindowDurationMins, primaryReset, time.Now())
		result.Windows = append(result.Windows, provider.UsageWindow{
			Name:        name,
			DisplayName: displayName,
			Utilization: *rl.Primary.UsedPercent,
			ResetsAt:    primaryReset,
		})
	}

	if validRateLimitWindow(rl.Secondary) {
		secondaryReset := time.Unix(rl.Secondary.ResetsAt, 0)
		name, displayName := codexWindowLabels(rl.Secondary.WindowDurationMins, secondaryReset, time.Now())
		result.Windows = append(result.Windows, provider.UsageWindow{
			Name:        name,
			DisplayName: displayName,
			Utilization: *rl.Secondary.UsedPercent,
			ResetsAt:    secondaryReset,
		})
	}
	if len(result.Windows) == 0 {
		result.Error = "no complete rate limit data"
	}

	return result, nil
}

// codexWindowLabels prefers Codex's declared duration when present. This is
// important when a weekly window is near its reset: its remaining time can be
// shorter than a five-hour window, but windowDurationMins still identifies it
// correctly. The reset-horizon fallback keeps compatibility with older
// responses that omitted the duration.
func codexWindowLabels(windowDurationMins int64, resetsAt, now time.Time) (string, string) {
	if windowDurationMins >= int64(24*time.Hour/time.Minute) {
		return "7d", "7 days"
	}
	if windowDurationMins > 0 {
		return "5h", "5h"
	}
	if resetsAt.After(now.Add(24 * time.Hour)) {
		return "7d", "7 days"
	}
	return "5h", "5h"
}

// Wire types

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rateLimitsResult struct {
	RateLimits *rateLimits `json:"rateLimits"`
}

type rateLimits struct {
	Primary   *rateLimitWindow `json:"primary"`
	Secondary *rateLimitWindow `json:"secondary"`
}

type rateLimitWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins int64    `json:"windowDurationMins"`
	ResetsAt           int64    `json:"resetsAt"`
}

func validRateLimitWindow(window *rateLimitWindow) bool {
	return window != nil && window.UsedPercent != nil && *window.UsedPercent >= 0 && *window.UsedPercent <= 100 && window.ResetsAt > 0
}

func writeJSON(w interface{ Write([]byte) (int, error) }, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func readResponse(scanner *bufio.Scanner) ([]byte, error) {
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var peek struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			continue
		}
		// Skip notifications (no id, has method)
		if peek.ID == nil && peek.Method != "" {
			continue
		}
		if peek.ID != nil {
			result := make([]byte, len(line))
			copy(result, line)
			return result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errNoResponse
}

type appServerError struct {
	stage   string
	err     error
	waitErr error
	stderr  string
}

func (e *appServerError) Error() string {
	msg := e.err.Error()
	if errors.Is(e.err, errNoResponse) {
		msg = "codex app-server exited without a response"
	}
	if e.stage != "" {
		msg = e.stage + ": " + msg
	}
	if e.waitErr != nil {
		msg += fmt.Sprintf(" (%v)", e.waitErr)
	}
	if e.stderr != "" {
		msg += ": " + truncateDiagnostic(redactDiagnostic(e.stderr), 160)
	}
	return msg
}

func (e *appServerError) Unwrap() error {
	return e.err
}

func isRetryableAppServerError(err error) bool {
	if errors.Is(err, errNoResponse) || provider.IsTransientFetchError(err.Error()) {
		return true
	}
	return false
}

func collectStderr(r io.Reader) <-chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		buf := make([]byte, 1024)
		var out strings.Builder
		for {
			n, err := r.Read(buf)
			if n > 0 && out.Len() < maxStderrBytes {
				remaining := maxStderrBytes - out.Len()
				if n > remaining {
					n = remaining
				}
				out.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		ch <- strings.TrimSpace(out.String())
	}()
	return ch
}

func redactDiagnostic(s string) string {
	words := strings.Fields(s)
	for i, word := range words {
		if strings.HasPrefix(word, "sk-") {
			words[i] = "[REDACTED]"
		}
	}
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}

func truncateDiagnostic(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// Register registers the Codex provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("openai")
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}

var _ provider.SourceCapability = (*Provider)(nil)
