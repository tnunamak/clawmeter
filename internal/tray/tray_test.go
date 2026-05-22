//go:build tray

package tray

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestIconMeterStateUsesActualExpectedAndProjectedRiskSeparately(t *testing.T) {
	now := time.Now()
	data := &provider.UsageData{
		Provider: "openai",
		Windows: []provider.UsageWindow{
			{
				Name:        "5h",
				Utilization: 90,
				ResetsAt:    now.Add(1 * time.Hour),
			},
			{
				Name:        "7d",
				Utilization: 50,
				ResetsAt:    now.Add(6 * 24 * time.Hour),
			},
		},
	}

	meter := iconMeterState(data)
	if !meter.ShowExpected {
		t.Fatal("meter should show expected pace for healthy usage windows")
	}
	if meter.UsagePct != 50 {
		t.Fatalf("UsagePct = %.1f, want actual utilization from worst projected window", meter.UsagePct)
	}
	wantExpected := 100 / 7.0
	if absFloat(meter.ExpectedPct-wantExpected) > 0.2 {
		t.Fatalf("ExpectedPct = %.1f, want roughly %.1f", meter.ExpectedPct, wantExpected)
	}
	if meter.RiskPct < 300 {
		t.Fatalf("RiskPct = %.1f, want projected overrun severity from rate", meter.RiskPct)
	}
}

func TestProviderSeverityUsesHighestProjectedUsage(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
			},
		},
	}

	keys := sortedKeys(results)
	if len(keys) == 0 {
		t.Fatal("sortedKeys() returned no providers")
	}
	if keys[0] != "openai" {
		t.Fatalf("sortedKeys()[0] = %q, want openai because it has the highest projected usage; keys=%v", keys[0], keys)
	}
}

func TestIconProviderOverrideCyclesThroughAutoAndActiveProviders(t *testing.T) {
	choices := []string{"claude", "openai"}

	if got := nextIconProviderOverride("", choices); got != "claude" {
		t.Fatalf("next from auto = %q, want claude", got)
	}
	if got := nextIconProviderOverride("claude", choices); got != "openai" {
		t.Fatalf("next from claude = %q, want openai", got)
	}
	if got := nextIconProviderOverride("openai", choices); got != "" {
		t.Fatalf("next from openai = %q, want auto", got)
	}
	if got := nextIconProviderOverride("missing", choices); got != "" {
		t.Fatalf("next from missing override = %q, want auto", got)
	}
}

func TestSelectedTrayProviderHonorsIconOverride(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"openai": {
			Provider: "openai",
			Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 44, ResetsAt: now.Add(4*24*time.Hour + 20*time.Hour)},
			},
		},
		"claude": {
			Provider: "claude",
			Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 77, ResetsAt: now.Add(87 * time.Minute)},
			},
		},
	}

	s.mu.Lock()
	s.iconProviderOverride = "claude"
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iconProviderOverride = ""
		s.mu.Unlock()
	}()

	name, _, ok := selectedTrayProvider(results)
	if !ok {
		t.Fatal("selectedTrayProvider returned no provider")
	}
	if name != "claude" {
		t.Fatalf("selected provider = %q, want override claude", name)
	}
}

func TestExpectedUsagePctClampsToResetWindow(t *testing.T) {
	if got := expectedUsagePct(time.Now().Add(forecast.SevenDayWindow+time.Hour), forecast.SevenDayWindow); got != 0 {
		t.Fatalf("future-before-window expected usage = %.1f, want 0", got)
	}
	if got := expectedUsagePct(time.Now().Add(-time.Hour), forecast.SevenDayWindow); got != 100 {
		t.Fatalf("past-reset expected usage = %.1f, want 100", got)
	}
}

func absFloat(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}
