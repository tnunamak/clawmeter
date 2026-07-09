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
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	timeout          = 15 * time.Second
	maxFetchAttempts = 2
	retryDelay       = 150 * time.Millisecond
	maxStderrBytes   = 4096
)

var errNoResponse = errors.New("no response received")

// Provider implements the provider.Provider interface for Codex.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new Codex provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func (p *Provider) Name() string         { return "openai" } // stable config key
func (p *Provider) DisplayName() string  { return "Codex" }
func (p *Provider) Description() string  { return "Codex quota (via codex CLI)" }
func (p *Provider) DashboardURL() string { return "https://platform.openai.com/usage" }

// FetchUsage retrieves rate limit data by launching codex as a JSON-RPC subprocess.
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	codexPath, err := codexExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("codex not found on PATH")
	}

	var lastErr error
	for attempt := 1; attempt <= maxFetchAttempts; attempt++ {
		data, err := p.fetchUsageOnce(ctx, codexPath)
		if err == nil {
			p.attachResetCredits(ctx, data)
			return data, nil
		}
		lastErr = err
		if attempt == maxFetchAttempts || !isRetryableAppServerError(err) || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (p *Provider) fetchUsageOnce(ctx context.Context, codexPath string) (*provider.UsageData, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, codexPath, "-s", "read-only", "-a", "untrusted", "app-server")
	hideSubprocessWindow(cmd)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

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
			Provider:  p.Name(),
			FetchedAt: time.Now(),
			Error:     msg,
		}, nil
	}

	if resp.Result == nil || resp.Result.RateLimits == nil {
		return &provider.UsageData{
			Provider:  p.Name(),
			FetchedAt: time.Now(),
			Error:     "no rate limit data",
		}, nil
	}

	rl := resp.Result.RateLimits
	result := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	if rl.Primary != nil {
		result.Windows = append(result.Windows, provider.UsageWindow{
			Name:        "5h",
			DisplayName: "5h",
			Utilization: rl.Primary.UsedPercent,
			ResetsAt:    time.Unix(rl.Primary.ResetsAt, 0),
		})
	}

	if rl.Secondary != nil {
		result.Windows = append(result.Windows, provider.UsageWindow{
			Name:        "7d",
			DisplayName: "7 days",
			Utilization: rl.Secondary.UsedPercent,
			ResetsAt:    time.Unix(rl.Secondary.ResetsAt, 0),
		})
	}

	return result, nil
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
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    int64   `json:"resetsAt"`
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
	return registry.Register(New(providerCfg))
}
