package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

type cliStubProvider struct {
	name string
}

func (p cliStubProvider) Name() string         { return p.name }
func (p cliStubProvider) DisplayName() string  { return p.name }
func (p cliStubProvider) Description() string  { return "" }
func (p cliStubProvider) DashboardURL() string { return "" }
func (p cliStubProvider) IsConfigured() bool   { return true }
func (p cliStubProvider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	return nil, nil
}

func TestAgentSummaryKeepsExactBoundarySeconds(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{
			Name:    "openai",
			Display: "OpenAI",
			Data: &provider.UsageData{Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 99, ResetsAt: now.Add(59 * time.Second)},
			}},
		},
	}}

	got := output.AgentSummary()
	for _, want := range []string{
		"Quota: worst=OpenAI 7d",
		"current=99%",
		"reset_in_seconds=",
		"reset_in=59s",
		"status=tight",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AgentSummary() = %q, missing %q", got, want)
		}
	}
}

func TestAgentSummaryIncludesAllUsableQuotas(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{
			Name:    "claude",
			Display: "Claude",
			Data: &provider.UsageData{Windows: []provider.UsageWindow{
				{Name: "5h Sonnet", Utilization: 40, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "7d All", Utilization: 74, ResetsAt: now.Add(36 * time.Hour)},
			}},
		},
		{
			Name:    "openai",
			Display: "OpenAI",
			Data: &provider.UsageData{Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 16, ResetsAt: now.Add(36 * time.Hour)},
			}},
		},
	}}

	got := output.AgentSummary()
	for _, want := range []string{
		"quotas=[",
		"Claude 7d All(",
		"Claude 5h Sonnet(",
		"OpenAI 7d(",
		"projected_at_reset=",
		"status=tight",
		"status=on_track",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AgentSummary() = %q, missing %q", got, want)
		}
	}

	claude7d := strings.Index(got, "Claude 7d All(")
	claude5h := strings.Index(got, "Claude 5h Sonnet(")
	openai7d := strings.Index(got, "OpenAI 7d(")
	if !(claude7d >= 0 && claude7d < claude5h && claude5h < openai7d) {
		t.Fatalf("AgentSummary() quota order = %q, want most-at-risk to least-at-risk", got)
	}
}

func TestFormatPrecisePctUsesMoreDecimalsNearBoundaries(t *testing.T) {
	tests := []struct {
		pct  float64
		want string
	}{
		{26, "26%"},
		{67.891, "67.9%"},
		{89.92, "89.92%"},
		{99, "99%"},
		{99.02, "99.02%"},
		{0.42, "0.42%"},
	}
	for _, tt := range tests {
		if got := formatPrecisePct(tt.pct); got != tt.want {
			t.Fatalf("formatPrecisePct(%v) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

func TestStatusLineSummaryIsCompact(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{
			Name:    "claude",
			Display: "Claude",
			Data: &provider.UsageData{Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
			}},
		},
	}}

	got := output.StatusLineSummary()
	for _, want := range []string{"CM", "Claude", "5h", "est.", "reset"} {
		if !strings.Contains(got, want) {
			t.Fatalf("StatusLineSummary() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "reset_in_seconds") {
		t.Fatalf("StatusLineSummary() should stay human compact, got %q", got)
	}
}

func TestClassifyProvider(t *testing.T) {
	now := time.Now()

	// classifyProvider uses forecast.Project which calls time.Until
	// internally, so there is microsecond drift. Use values that are
	// clearly in one tier, not on boundaries.
	tests := []struct {
		name            string
		pf              ProviderFormatter
		wantTier        int
		wantProjectedLo float64 // lower bound for maxProjectedPct (0 to skip check)
		wantProjectedHi float64 // upper bound for maxProjectedPct (0 to skip check)
	}{
		{
			name:     "passive nil data → tier 5 (unavailable)",
			pf:       ProviderFormatter{Name: "test", Data: nil},
			wantTier: 5,
		},
		{
			name:     "explicit nil data → tier 1 (actionable setup issue)",
			pf:       ProviderFormatter{Name: "test", Data: nil, ExplicitlyEnabled: true},
			wantTier: 1,
		},
		{
			name: "expired data → tier 0",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{IsExpired: true},
			},
			wantTier:        0,
			wantProjectedLo: 100,
			wantProjectedHi: 100,
		},
		{
			name: "errored with no windows → tier 1",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{Error: "connection refused"},
			},
			wantTier: 1,
		},
		{
			name: "stale windows → tier 3",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{
					Stale:   true,
					Warning: "usage unavailable",
					Windows: []provider.UsageWindow{
						// 10% with 4h remaining of 5h: elapsed=1h, rate=10/3600, projected=50
						{Name: "5h", Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
					},
				},
			},
			wantTier:        3, // stale-but-readable data should be visible as a warning
			wantProjectedLo: 45,
			wantProjectedHi: 55,
		},
		{
			name: "high projected → tier 2 (critical)",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{
					Windows: []provider.UsageWindow{
						// 90% with 1h remaining of 5h: elapsed=4h, rate=90/14400, projected=112.5
						{Name: "5h", Utilization: 90, ResetsAt: now.Add(1 * time.Hour)},
					},
				},
			},
			wantTier:        2,
			wantProjectedLo: 110,
			wantProjectedHi: 115,
		},
		{
			name: "moderate projected (~94%) → tier 3 (warning)",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{
					Windows: []provider.UsageWindow{
						// 75% with 1h remaining of 5h: elapsed=4h, rate=75/14400, projected=93.75
						{Name: "5h", Utilization: 75, ResetsAt: now.Add(1 * time.Hour)},
					},
				},
			},
			wantTier:        3,
			wantProjectedLo: 92,
			wantProjectedHi: 95,
		},
		{
			name: "low projected (50%) → tier 4 (healthy)",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{
					Windows: []provider.UsageWindow{
						// 25% with 2.5h remaining of 5h: elapsed=2.5h, projected=50
						{Name: "5h", Utilization: 25, ResetsAt: now.Add(150 * time.Minute)},
					},
				},
			},
			wantTier:        4,
			wantProjectedLo: 49,
			wantProjectedHi: 51,
		},
		{
			name: "multiple windows — worst window determines tier",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{
					Windows: []provider.UsageWindow{
						// healthy window: 10% with 4h remaining → projected ~50
						{Name: "5h", Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
						// critical window: 95% with 30min remaining of 5h → projected ~105.6
						{Name: "5h", Utilization: 95, ResetsAt: now.Add(30 * time.Minute)},
					},
				},
			},
			wantTier:        2,
			wantProjectedLo: 105,
			wantProjectedHi: 110,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := classifyProvider(&tt.pf)
			if u.tier != tt.wantTier {
				t.Errorf("tier = %d, want %d (maxProjectedPct=%.2f)", u.tier, tt.wantTier, u.maxProjectedPct)
			}
			if tt.wantProjectedHi > 0 {
				if u.maxProjectedPct < tt.wantProjectedLo || u.maxProjectedPct > tt.wantProjectedHi {
					t.Errorf("maxProjectedPct = %.2f, want [%.0f, %.0f]", u.maxProjectedPct, tt.wantProjectedLo, tt.wantProjectedHi)
				}
			}
		})
	}
}

func TestSortProvidersByUrgency(t *testing.T) {
	now := time.Now()

	t.Run("sorts by tier: expired, errored, critical, warning, healthy", func(t *testing.T) {
		providers := []ProviderFormatter{
			{Name: "healthy", Display: "Healthy", Data: &provider.UsageData{
				Windows: []provider.UsageWindow{
					// 10% with 4h remaining → projected ~50 → tier 4
					{Name: "5h", Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
				},
			}},
			{Name: "expired", Display: "Expired", Data: &provider.UsageData{IsExpired: true}},
			{Name: "critical", Display: "Critical", Data: &provider.UsageData{
				Windows: []provider.UsageWindow{
					// 90% with 1h remaining → projected ~112.5 → tier 2
					{Name: "5h", Utilization: 90, ResetsAt: now.Add(1 * time.Hour)},
				},
			}},
			{Name: "errored", Display: "Errored", Data: &provider.UsageData{Error: "fail"}},
			{Name: "warning", Display: "Warning", Data: &provider.UsageData{
				Windows: []provider.UsageWindow{
					// 75% with 1h remaining → projected ~93.75 → tier 3
					{Name: "5h", Utilization: 75, ResetsAt: now.Add(1 * time.Hour)},
				},
			}},
		}

		sortProvidersByUrgency(providers)

		wantOrder := []string{"expired", "errored", "critical", "warning", "healthy"}
		for i, want := range wantOrder {
			if providers[i].Name != want {
				t.Errorf("position %d: got %q, want %q", i, providers[i].Name, want)
			}
		}
	})

	t.Run("within same tier, higher projected usage first", func(t *testing.T) {
		providers := []ProviderFormatter{
			{Name: "low", Display: "Low", Data: &provider.UsageData{
				Windows: []provider.UsageWindow{
					// 10% with 4h remaining → projected ~50 → tier 4
					{Name: "5h", Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
				},
			}},
			{Name: "high", Display: "High", Data: &provider.UsageData{
				Windows: []provider.UsageWindow{
					// 40% with 2h remaining → projected ~66.7 → tier 4
					{Name: "5h", Utilization: 40, ResetsAt: now.Add(2 * time.Hour)},
				},
			}},
		}

		sortProvidersByUrgency(providers)

		if providers[0].Name != "high" {
			t.Errorf("expected 'high' first, got %q", providers[0].Name)
		}
		if providers[1].Name != "low" {
			t.Errorf("expected 'low' second, got %q", providers[1].Name)
		}
	})

	t.Run("stable sort: same tier and projected pct maintain relative order", func(t *testing.T) {
		// Two nil-data providers (both tier 1, both projected 0)
		providers := []ProviderFormatter{
			{Name: "alpha", Display: "Alpha", Data: nil},
			{Name: "beta", Display: "Beta", Data: nil},
		}

		sortProvidersByUrgency(providers)

		if providers[0].Name != "alpha" {
			t.Errorf("expected 'alpha' first (stable), got %q", providers[0].Name)
		}
		if providers[1].Name != "beta" {
			t.Errorf("expected 'beta' second (stable), got %q", providers[1].Name)
		}
	})
}

func TestHideUnavailable_HidesAutoDetectedErrorsButKeepsExplicit(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(cliStubProvider{name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	result := &provider.MultiFetchResult{
		Results: map[string]*provider.UsageData{
			"alpha": {Provider: "alpha", Error: "forbidden"},
		},
	}

	autoCfg := config.DefaultConfig()
	autoOutput := buildOutputFromResult(registry, autoCfg, result, nil)
	autoOutput.HideUnavailable()
	if len(autoOutput.Providers) != 0 {
		t.Fatalf("auto-detected error should be hidden, got %d providers", len(autoOutput.Providers))
	}

	explicitCfg := config.DefaultConfig()
	explicitCfg.EnsureProvider("alpha", true)
	explicitOutput := buildOutputFromResult(registry, explicitCfg, result, nil)
	explicitOutput.HideUnavailable()
	if len(explicitOutput.Providers) != 1 {
		t.Fatalf("explicitly enabled error should remain visible, got %d providers", len(explicitOutput.Providers))
	}
}

func TestStaleFallbackMarksLastGoodDataAndDropsResetlessWindows(t *testing.T) {
	entry := &cache.Entry{
		ProviderData: map[string]*provider.UsageData{
			"claude": {
				Provider:  "claude",
				FetchedAt: time.Date(2026, 5, 28, 15, 4, 0, 0, time.Local),
				Windows: []provider.UsageWindow{
					{Name: "7d Sonnet", Utilization: 0},
					{Name: "7d All", Utilization: 12, ResetsAt: time.Now().Add(24 * time.Hour)},
				},
			},
		},
	}

	got, ok := staleFallback(entry, "claude", "usage unavailable")
	if !ok {
		t.Fatal("staleFallback() ok = false, want true")
	}
	if !got.Stale || got.Warning != "usage unavailable" || got.Error != "" {
		t.Fatalf("fallback state = stale:%v warning:%q error:%q", got.Stale, got.Warning, got.Error)
	}
	if len(got.Windows) != 1 || got.Windows[0].Name != "7d All" {
		t.Fatalf("fallback windows = %+v, want only usable last-good window", got.Windows)
	}
	if entry.ProviderData["claude"].Stale {
		t.Fatal("staleFallback mutated cache entry")
	}
}
