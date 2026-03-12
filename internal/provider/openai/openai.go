// Package openai implements the Provider interface for OpenAI/Codex via JSON-RPC subprocess.
package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

const timeout = 15 * time.Second

// Provider implements the provider.Provider interface for OpenAI/Codex.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new OpenAI provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func (p *Provider) Name() string         { return "openai" }
func (p *Provider) DisplayName() string  { return "OpenAI" }
func (p *Provider) Description() string  { return "OpenAI/Codex (via codex CLI)" }
func (p *Provider) DashboardURL() string { return "https://platform.openai.com/usage" }

// IsConfigured returns true if the codex CLI is available on PATH.
func (p *Provider) IsConfigured() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// FetchUsage retrieves rate limit data by launching codex as a JSON-RPC subprocess.
func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex not found on PATH")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, codexPath, "-s", "read-only", "-a", "untrusted", "app-server")
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}
	defer func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

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
		return nil, fmt.Errorf("send initialize: %w", err)
	}

	if _, err := readResponse(scanner); err != nil {
		return nil, fmt.Errorf("read initialize response: %w", err)
	}

	// Step 2: initialized notification
	if err := writeJSON(stdin, map[string]interface{}{
		"method": "initialized",
		"params": map[string]interface{}{},
	}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	// Step 3: account/read — check auth type
	if err := writeJSON(stdin, map[string]interface{}{
		"id":     2,
		"method": "account/read",
		"params": map[string]interface{}{},
	}); err != nil {
		return nil, fmt.Errorf("send account/read: %w", err)
	}

	acctData, err := readResponse(scanner)
	if err != nil {
		return nil, fmt.Errorf("read account response: %w", err)
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
		return nil, fmt.Errorf("send rateLimits: %w", err)
	}

	respData, err := readResponse(scanner)
	if err != nil {
		return nil, fmt.Errorf("read rateLimits response: %w", err)
	}

	return p.parseRateLimits(respData, acct)
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
	return nil, fmt.Errorf("no response received")
}

// Register registers the OpenAI provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("openai")
	return registry.Register(New(providerCfg))
}
