package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

type fakeProvider struct {
	name        string
	display     string
	description string
	setup       provider.SetupStatus
	data        *provider.UsageData
	err         error
	fetchCalls  int
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) DisplayName() string {
	if p.display != "" {
		return p.display
	}
	return "Fake"
}
func (p *fakeProvider) Description() string {
	if p.description != "" {
		return p.description
	}
	return "Fake quota (via test credentials)"
}
func (p *fakeProvider) DashboardURL() string              { return "" }
func (p *fakeProvider) IsConfigured() bool                { return p.setup.IsReady() }
func (p *fakeProvider) SetupStatus() provider.SetupStatus { return p.setup }
func (p *fakeProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	p.fetchCalls++
	return p.data, p.err
}

func TestRunSummarizesSuccessfulProbe(t *testing.T) {
	reset := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	p := &fakeProvider{
		name:  "openai",
		setup: provider.SetupStatus{State: provider.SetupReady, Detail: "ChatGPT account"},
		data: &provider.UsageData{
			Provider:     "openai",
			FetchedAt:    reset.Add(-time.Hour),
			Windows:      []provider.UsageWindow{{Name: "7d", Utilization: 42.5, ResetsAt: reset}},
			ResetCredits: &provider.UsageResetCredits{AvailableCount: 3},
		},
	}

	out := Run(context.Background(), []provider.Provider{p}, func(string) string { return "detected" }, func(string) bool { return true })
	if p.fetchCalls != 1 {
		t.Fatalf("FetchUsage calls = %d, want 1", p.fetchCalls)
	}
	diagnostic := out.Diagnostics[0]
	if diagnostic.Probe.Outcome != "success" || diagnostic.Usage == nil {
		t.Fatalf("diagnostic = %#v, want successful usage", diagnostic)
	}
	if len(diagnostic.Usage.Windows) != 1 || diagnostic.Usage.Windows[0].Utilization != 42.5 {
		t.Fatalf("windows = %#v", diagnostic.Usage.Windows)
	}
	if diagnostic.Usage.ResetCreditsAvailable == nil || *diagnostic.Usage.ResetCreditsAvailable != 3 {
		t.Fatalf("reset credits = %#v", diagnostic.Usage.ResetCreditsAvailable)
	}
}

func TestRunDoesNotProbeUnreadyOrInactiveProvider(t *testing.T) {
	unready := &fakeProvider{name: "unready", setup: provider.SetupStatus{State: provider.SetupNeedsAuth}}
	inactive := &fakeProvider{name: "inactive", setup: provider.SetupStatus{State: provider.SetupReady}}

	out := Run(
		context.Background(),
		[]provider.Provider{unready, inactive},
		func(string) string { return "setup_needed" },
		func(name string) bool { return name != "inactive" },
	)
	if unready.fetchCalls != 0 || inactive.fetchCalls != 0 {
		t.Fatalf("unexpected probes: unready=%d inactive=%d", unready.fetchCalls, inactive.fetchCalls)
	}
	for _, diagnostic := range out.Diagnostics {
		if diagnostic.Probe.Attempted || diagnostic.Probe.Outcome != "skipped" {
			t.Fatalf("probe = %#v, want skipped", diagnostic.Probe)
		}
	}
}

func TestDiagnosticNeverSerializesRawError(t *testing.T) {
	secret := "user@example.com bearer-secret account-123"
	p := &fakeProvider{
		name:  "openai",
		setup: provider.SetupStatus{State: provider.SetupReady},
		err:   errors.New("HTTP 401 for " + secret),
	}
	out := Run(context.Background(), []provider.Provider{p}, func(string) string { return "detected" }, func(string) bool { return true })
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "example.com") {
		t.Fatalf("diagnostic leaked raw error: %s", encoded)
	}
	probe := out.Diagnostics[0].Probe
	if probe.ErrorCategory != "auth" || probe.Message != "Authentication was rejected or has expired." {
		t.Fatalf("probe = %#v", probe)
	}
}

func TestDiagnosticOmitsProviderControlledLabels(t *testing.T) {
	sensitive := "account@example.com / organization-123"
	p := &fakeProvider{
		name:        "kimi",
		display:     sensitive,
		description: sensitive,
		setup:       provider.SetupStatus{State: provider.SetupReady},
		data: &provider.UsageData{
			Provider: "kimi",
			Windows:  []provider.UsageWindow{{Name: sensitive, DisplayName: sensitive, Utilization: 10}},
			Balances: []provider.UsageBalance{{Name: sensitive, DisplayName: sensitive, Remaining: 2}},
		},
	}
	out := Run(context.Background(), []provider.Provider{p}, func(string) string { return "detected" }, func(string) bool { return true })
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), sensitive) || strings.Contains(string(encoded), "example.com") {
		t.Fatalf("diagnostic leaked provider-controlled label: %s", encoded)
	}
	if out.Diagnostics[0].Usage.Windows[0].Index != 1 || out.Diagnostics[0].Usage.Windows[0].Name != "" {
		t.Fatalf("unsafe window label was not reduced to its ordinal: %#v", out.Diagnostics[0].Usage.Windows[0])
	}
}

func TestSafeErrorCategories(t *testing.T) {
	tests := map[string]string{
		"429 rate limited":             "rate_limited",
		"connection reset by peer":     "network",
		"decode response: bad payload": "parse",
		"API returned HTTP 503":        "api",
		"unexpected failure":           "unknown",
	}
	for input, want := range tests {
		got, _ := safeError(input)
		if got != want {
			t.Errorf("safeError(%q) = %q, want %q", input, got, want)
		}
	}
}
