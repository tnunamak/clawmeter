// Package diagnose builds privacy-safe provider diagnostics.
package diagnose

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const SchemaVersion = 1

type Output struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

type Diagnostic struct {
	Provider     string                    `json:"provider"`
	Maturity     provider.ProviderMaturity `json:"maturity"`
	Setup        Setup                     `json:"setup"`
	PollingState string                    `json:"polling_state"`
	Probe        Probe                     `json:"probe"`
	Usage        *UsageSummary             `json:"usage,omitempty"`
}

type Setup struct {
	State  provider.SetupState `json:"state"`
	Detail string              `json:"detail,omitempty"`
}

type Probe struct {
	Attempted     bool   `json:"attempted"`
	Outcome       string `json:"outcome"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	ErrorCategory string `json:"error_category,omitempty"`
	Message       string `json:"message,omitempty"`
}

type UsageSummary struct {
	FetchedAt             *time.Time       `json:"fetched_at,omitempty"`
	Stale                 bool             `json:"stale"`
	Windows               []WindowSummary  `json:"windows,omitempty"`
	Balances              []BalanceSummary `json:"balances,omitempty"`
	ResetCreditsAvailable *int             `json:"reset_credits_available,omitempty"`
}

type WindowSummary struct {
	Index       int        `json:"index"`
	Name        string     `json:"name,omitempty"`
	Utilization float64    `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

type BalanceSummary struct {
	Index     int     `json:"index"`
	Remaining float64 `json:"remaining"`
}

var safeWindowNames = map[string]bool{
	"5h": true, "7d": true, "7d All": true, "7d OAuth": true,
	"7d Opus": true, "7d Sonnet": true, "7d Fable": true,
	"24h Pro": true, "24h Flash": true, "daily": true, "weekly": true,
	"monthly": true, "premium": true, "chat": true, "credits": true,
	"key": true, "bonus": true, "extra": true,
}

// Run diagnoses providers in the supplied order. shouldProbe is policy owned
// by the caller: a named diagnostic can probe an explicitly selected provider,
// while an all-provider diagnostic should probe only normally polled providers.
func Run(
	ctx context.Context,
	providers []provider.Provider,
	pollingState func(string) string,
	shouldProbe func(string) bool,
) Output {
	out := Output{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now(),
		Diagnostics:   make([]Diagnostic, len(providers)),
	}
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.Diagnostics[i] = runOne(ctx, p, pollingState, shouldProbe)
		}()
	}
	wg.Wait()
	return out
}

func runOne(
	ctx context.Context,
	p provider.Provider,
	pollingState func(string) string,
	shouldProbe func(string) bool,
) Diagnostic {
	setup := provider.GetSetupStatus(p)
	diagnostic := Diagnostic{
		Provider:     p.Name(),
		Maturity:     provider.GetMaturity(p.Name()),
		Setup:        Setup{State: setup.State, Detail: safeSetupDetail(setup.State)},
		PollingState: pollingState(p.Name()),
		Probe:        Probe{Outcome: "skipped"},
	}
	if !setup.IsReady() {
		diagnostic.Probe.Message = "Provider setup is not ready."
	} else if !shouldProbe(p.Name()) {
		diagnostic.Probe.Message = "Provider is not enabled for polling."
	} else {
		diagnostic.Probe, diagnostic.Usage = probe(ctx, p)
	}
	return diagnostic
}

func safeSetupDetail(state provider.SetupState) string {
	switch state {
	case provider.SetupReady:
		return "Supported credentials detected."
	case provider.SetupNeedsAuth:
		return "Authentication setup is required."
	default:
		return "No supported credentials detected."
	}
}

func probe(ctx context.Context, p provider.Provider) (Probe, *UsageSummary) {
	started := time.Now()
	data, err := p.FetchUsage(ctx)
	result := Probe{Attempted: true, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		result.Outcome = "error"
		result.ErrorCategory, result.Message = safeError(err.Error())
		return result, nil
	}
	if data == nil {
		result.Outcome = "error"
		result.ErrorCategory = "parse"
		result.Message = safeMessage("parse")
		return result, nil
	}
	if data.Error != "" || data.IsExpired {
		result.Outcome = "error"
		result.ErrorCategory, result.Message = safeError(data.Error)
		if data.IsExpired {
			result.ErrorCategory = "auth"
			result.Message = safeMessage("auth")
		}
		return result, nil
	}
	result.Outcome = "success"
	return result, summarizeUsage(data)
}

func summarizeUsage(data *provider.UsageData) *UsageSummary {
	summary := &UsageSummary{Stale: data.Stale}
	if !data.FetchedAt.IsZero() {
		fetchedAt := data.FetchedAt
		summary.FetchedAt = &fetchedAt
	}
	for i, window := range data.PresentationWindows() {
		windowSummary := WindowSummary{Index: i + 1, Utilization: window.Utilization}
		if safeWindowNames[window.Name] {
			windowSummary.Name = window.Name
		}
		if !window.ResetsAt.IsZero() {
			resetsAt := window.ResetsAt
			windowSummary.ResetsAt = &resetsAt
		}
		summary.Windows = append(summary.Windows, windowSummary)
	}
	for i, balance := range data.Balances {
		summary.Balances = append(summary.Balances, BalanceSummary{
			Index: i + 1, Remaining: balance.Remaining,
		})
	}
	if data.ResetCredits != nil {
		count := data.ResetCredits.DisplayCount(time.Now())
		summary.ResetCreditsAvailable = &count
	}
	return summary
}

func safeError(raw string) (string, string) {
	lowered := strings.ToLower(raw)
	category := "unknown"
	switch {
	case strings.Contains(lowered, "rate limit"), strings.Contains(lowered, "429"):
		category = "rate_limited"
	case strings.Contains(lowered, "unauthor"), strings.Contains(lowered, "forbidden"),
		strings.Contains(lowered, "expired"), strings.Contains(lowered, "token"),
		strings.Contains(lowered, "credential"), strings.Contains(lowered, "401"),
		strings.Contains(lowered, "403"):
		category = "auth"
	case strings.Contains(lowered, "timeout"), strings.Contains(lowered, "deadline"),
		strings.Contains(lowered, "connection"), strings.Contains(lowered, "network"),
		strings.Contains(lowered, "no response"), strings.Contains(lowered, "eof"):
		category = "network"
	case strings.Contains(lowered, "decode"), strings.Contains(lowered, "parse"),
		strings.Contains(lowered, "malformed"), strings.Contains(lowered, "missing"):
		category = "parse"
	case strings.Contains(lowered, "api"), strings.Contains(lowered, "http"),
		strings.Contains(lowered, "server"), strings.Contains(lowered, "500"),
		strings.Contains(lowered, "502"), strings.Contains(lowered, "503"):
		category = "api"
	}
	return category, safeMessage(category)
}

func safeMessage(category string) string {
	switch category {
	case "auth":
		return "Authentication was rejected or has expired."
	case "rate_limited":
		return "The provider rate-limited this diagnostic."
	case "network":
		return "The provider could not be reached."
	case "parse":
		return "The provider response was not understood."
	case "api":
		return "The provider API returned an error."
	default:
		return "The provider diagnostic failed."
	}
}
