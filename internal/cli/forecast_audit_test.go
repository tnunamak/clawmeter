package cli

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestAuditM1LegacyForecastMatchesSnapshot(t *testing.T) {
	for _, now := range []time.Time{time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), time.Now()} {
		for _, ids := range [][]string{{"default"}, {"default", "work"}, {"one", "two"}} {
			for _, age := range []time.Duration{11 * time.Minute, 2 * time.Minute} {
				output := MultiProviderOutput{PollInterval: 300}
				for _, id := range ids {
					data := &provider.UsageData{Provider: "claude", SourceID: id, Stale: true, FetchedAt: now.Add(-age), Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 80, ResetsAt: now.Add(3 * time.Hour)}}}
					output.Providers = append(output.Providers, ProviderFormatter{Name: "claude:" + id, Family: "claude", SourceID: id, SourceCount: len(ids), Data: data})
				}
				raw, err := output.JSON(nil, now)
				if err != nil {
					t.Fatal(err)
				}
				var doc JSONOutput
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				p := doc.Providers["claude"]
				check := func(f *JSONForecast, v *JSONAccountView) {
					t.Helper()
					if f == nil || v == nil {
						t.Fatalf("missing forecast/view: %s", raw)
					}
					got := f.Windows["5h"]
					w := v.Windows[0]
					if age > 10*time.Minute {
						if got.Indicator != "stale" || got.ProjectedPct != 0 || w.Forecast.State != "stale" || w.Forecast.Pct != nil {
							t.Errorf("old sample: legacy=%+v view=%+v", got, w.Forecast)
						}
					} else if w.Forecast.Pct == nil || got.Indicator == "stale" || got.Indicator == "unknown" || math.Abs(got.ProjectedPct-203.39) > 1e-9 || got.ProjectedPct != *w.Forecast.Pct {
						t.Errorf("retained sample: legacy=%+v view=%+v", got, w.Forecast)
					}
				}
				if p.View != nil {
					check(p.Forecast, p.View)
				}
				for _, src := range p.Sources {
					check(src.Forecast, src.View)
				}
			}
		}
	}
}

func TestAuditM3CachedJSONCountsDown(t *testing.T) {
	now := time.Now()
	data := &provider.UsageData{Provider: "claude", SourceID: "default", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 80, ResetsAt: now.Add(3 * time.Hour)}}}
	registry := provider.NewRegistry()
	if err := registry.Register(sourcedCLIProvider{id: "default", revision: "test"}); err != nil {
		t.Fatal(err)
	}
	entry := &cache.Entry{FetchedAt: now, ProviderData: map[string]*provider.UsageData{"claude": data}, SourceRevisions: map[string]string{"claude": "test"}}
	output := buildOutputFromCache(registry, config.DefaultConfig(), entry)
	if len(output.Providers) != 1 || output.Providers[0].Data == nil || output.Providers[0].Data.Stale {
		t.Fatalf("expected successful cached sample: %+v", output.Providers)
	}
	raw, err := output.JSON(entry, now.Add(45*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var doc JSONOutput
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	w := doc.Providers["claude"].View.Windows[0]
	if w.Forecast.Pct == nil || *w.Forecast.Pct != 200 || w.Exhaustion.RunsOutInSeconds == nil || *w.Exhaustion.RunsOutInSeconds != 1755 {
		t.Fatalf("cached forecast drifted: %+v", w)
	}
}

func TestAuditM4MachineForecastNamesAverageBasis(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		used    float64
		elapsed time.Duration
		pct     float64
	}{{30, 60 * time.Hour, 84}, {5, time.Hour, 840}, {5, 13 * time.Hour, 840.0 / 13}} {
		data := &provider.UsageData{Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "7d", Utilization: tc.used, ResetsAt: now.Add(7*24*time.Hour - tc.elapsed)}}}
		output := MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", Family: "claude", Data: data}}}
		v := output.buildView(now)
		w := v.snap.Accounts[0].Windows[0]
		if math.Abs(w.Forecast.Pct-tc.pct) > 1e-9 {
			t.Fatalf("average changed: %v want %v", w.Forecast.Pct, tc.pct)
		}
		if got := v.agentLine(); !strings.Contains(got, "forecast_basis=average_since_reset") {
			t.Errorf("machine line hides basis: %s", got)
		}
		raw, err := output.JSON(nil, now)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		forecast := jpath(t, doc, "providers", "claude", "view", "windows", 0, "forecast").(map[string]any)
		if forecast["basis"] != "average_since_reset" {
			t.Errorf("JSON hides basis: %+v", forecast)
		}
	}
}

func TestAuditN1PercentageMeaningAcrossSurfaces(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		used  float64
		pct   string
		state view.ExhaustionState
	}{{39.84, "99.6%", view.ExhaustionLasts}, {40.16, "100.4%", view.ExhaustionRunsOut}, {5, "13%", view.ExhaustionLasts}, {99.6, "249%", view.ExhaustionRunsOut}} {
		data := &provider.UsageData{Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: tc.used, ResetsAt: now.Add(3 * time.Hour)}}}
		output := MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", Family: "claude", Data: data}}}
		v := output.buildView(now)
		w := v.snap.Accounts[0].Windows[0]
		texts := []string{v.plainLine(0), v.statusLine(), tray.Tooltip(v.snap, map[string]*provider.UsageData{"claude": data}), strings.Join(v.colorTable(), "\n"), strings.Join(tray.MenuRows(v.snap, 8)["claude"], "\n")}
		for _, got := range texts {
			if !strings.Contains(got, tc.pct) {
				t.Errorf("projection want %s in %q", tc.pct, got)
			}
		}
		if w.Exhaustion.State != tc.state {
			t.Errorf("state=%s want %s", w.Exhaustion.State, tc.state)
		}
		raw, err := output.JSON(nil, now)
		if err != nil {
			t.Fatal(err)
		}
		var doc JSONOutput
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		jw := doc.Providers["claude"].View.Windows[0]
		if jw.Forecast.Pct == nil || *jw.Forecast.Pct != roundPct(w.Forecast.Pct) || jw.Exhaustion.State != string(tc.state) {
			t.Fatalf("JSON lost numeric value or state: %+v", jw)
		}
		if tc.used == 99.6 {
			if row := w.Row(false); row.Used != "99.6%" {
				t.Errorf("current rounded to out: %+v", row)
			}
			if got := v.plainLine(0); !strings.Contains(got, "5h: 99.6%") {
				t.Errorf("current rounded to out: %s", got)
			}
			if got := tray.Tooltip(v.snap, map[string]*provider.UsageData{"claude": data}); !strings.Contains(got, "Current 99.6%") {
				t.Errorf("current rounded to out: %s", got)
			}
		}
	}
}

func TestAuditN2SubminuteAcrossSurfaces(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	for _, w := range []provider.UsageWindow{
		{Name: "5h", Utilization: 99.8, ResetsAt: now.Add(3 * time.Hour)},
		{Name: "5h", Utilization: 10, ResetsAt: now.Add(14 * time.Second)},
	} {
		data := &provider.UsageData{Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{w}}
		output := MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", Family: "claude", Data: data}}}
		v := output.buildView(now)
		for _, got := range []string{v.plainLine(0), v.statusLine(), tray.Tooltip(v.snap, map[string]*provider.UsageData{"claude": data})} {
			if !strings.Contains(got, "<1m") || strings.Contains(got, "in 0m") || strings.Contains(got, "out now") {
				t.Errorf("positive countdown: %s", got)
			}
		}
	}
}
