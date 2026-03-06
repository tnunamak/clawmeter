package cli

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

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
			name:     "nil data → tier 1 (errored)",
			pf:       ProviderFormatter{Name: "test", Data: nil},
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
			name: "errored with windows → classifies by windows not error",
			pf: ProviderFormatter{
				Name: "test",
				Data: &provider.UsageData{
					Error: "showing cached",
					Windows: []provider.UsageWindow{
						// 10% with 4h remaining of 5h: elapsed=1h, rate=10/3600, projected=50
						{Name: "5h", Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
					},
				},
			},
			wantTier:        4, // healthy: projected ~50%
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
