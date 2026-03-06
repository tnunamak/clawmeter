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

func (p *Provider) Name() string        { return "openai" }
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

	// Step 1: Send initialize request
	initReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "clawmeter",
				"version": "1.0.0",
			},
		},
	}

	if err := writeJSON(stdin, &initReq); err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}

	// Read initialize response
	if _, err := readResponse(scanner); err != nil {
		return nil, fmt.Errorf("read initialize response: %w", err)
	}

	// Step 2: Send initialized notification
	initNotif := jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if err := writeJSON(stdin, &initNotif); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	// Step 3: Send account/rateLimits/read request
	rateLimitsReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "account/rateLimits/read",
	}
	if err := writeJSON(stdin, &rateLimitsReq); err != nil {
		return nil, fmt.Errorf("send rateLimits: %w", err)
	}

	// Read rate limits response
	respData, err := readResponse(scanner)
	if err != nil {
		return nil, fmt.Errorf("read rateLimits response: %w", err)
	}

	return p.parseRateLimits(respData)
}

func (p *Provider) parseRateLimits(data []byte) (*provider.UsageData, error) {
	var resp struct {
		Result *rateLimitsResult `json:"result"`
		Error  *jsonRPCError     `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if resp.Error != nil {
		return &provider.UsageData{
			Provider:  p.Name(),
			FetchedAt: time.Now(),
			Error:     resp.Error.Message,
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
		w := provider.UsageWindow{
			Name:        "session",
			DisplayName: "Session",
			Utilization: rl.Primary.UsedPercent,
			ResetsAt:    time.Now().Add(5 * time.Hour), // default
		}
		if rl.Primary.ResetDescription != "" {
			w.DisplayName = fmt.Sprintf("Session (%s)", rl.Primary.ResetDescription)
		}
		result.Windows = append(result.Windows, w)
	}

	if rl.Secondary != nil {
		w := provider.UsageWindow{
			Name:        "weekly",
			DisplayName: "Weekly",
			Utilization: rl.Secondary.UsedPercent,
			ResetsAt:    time.Now().Add(7 * 24 * time.Hour),
		}
		if rl.Secondary.ResetDescription != "" {
			w.DisplayName = fmt.Sprintf("Weekly (%s)", rl.Secondary.ResetDescription)
		}
		result.Windows = append(result.Windows, w)
	}

	return result, nil
}

// JSON-RPC types

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

type jsonRPCError struct {
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
	UsedPercent      float64 `json:"usedPercent"`
	ResetDescription string  `json:"resetDescription"`
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
		// Check if this is a JSON-RPC response (has "id" field)
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
			// Copy bytes — scanner.Bytes() is only valid until next Scan()
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
