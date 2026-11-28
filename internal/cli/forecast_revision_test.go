package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray"
)

func TestRevisionHumanForecastBasis(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	data := &provider.UsageData{Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "7d", Utilization: 5, ResetsAt: now.Add(167 * time.Hour)}}}
	output := MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", Display: "Claude", Data: data}}}
	output.Providers = append(output.Providers, ProviderFormatter{Name: "openai", Display: "Codex", Data: &provider.UsageData{Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 10, ResetsAt: now.Add(3 * time.Hour)}}}})
	v := output.buildView(now)
	for name, text := range map[string]string{
		"plain": v.plainLine(0), "statusline": v.statusLine(),
		"table":   strings.Join(v.colorTable(), "\n"),
		"menu":    strings.Join(tray.MenuRows(v.snap, 8)["claude"], "\n"),
		"summary": captureStdout(t, func() { v.printSummary(false) }),
	} {
		if !strings.Contains(text, "average since reset") {
			t.Errorf("%s hides the rate basis: %s", name, text)
		}
	}
}

func TestRevisionAgentPreservesThresholdSide(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		used              float64
		projected, status string
	}{
		{35.99996, "<90%", "on_track"}, {36.00004, ">90%", "tight"},
		{39.99996, "<100%", "tight"}, {40.00004, ">100%", "at_risk"},
	} {
		data := &provider.UsageData{Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: tc.used, ResetsAt: now.Add(3 * time.Hour)}}}
		output := MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", Data: data}}}
		text := output.buildView(now).agentLine()
		if !strings.Contains(text, "projected_at_reset="+tc.projected) || !strings.Contains(text, "status="+tc.status) {
			t.Errorf("threshold side lost: %s", text)
		}
	}
	for _, tc := range []struct {
		pct  float64
		want string
	}{{99.9999, "<100%"}, {100.0001, ">100%"}, {89.9999, "<90%"}, {90.0001, ">90%"}, {12.5, "12.5%"}, {0.12, "0.12%"}} {
		if got := formatPrecisePct(tc.pct); got != tc.want {
			t.Errorf("precise percentage %v=%q want %q", tc.pct, got, tc.want)
		}
	}
}

func TestRevisionUnknownMonthlyLegacyIndicator(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	for _, ids := range [][]string{{"default"}, {"default", "work"}, {"one", "two"}} {
		output := MultiProviderOutput{}
		for _, id := range ids {
			data := &provider.UsageData{Provider: "alibaba", SourceID: id, FetchedAt: now, Windows: []provider.UsageWindow{{Name: "monthly", LengthUnknown: true, Utilization: 9, ResetsAt: now.Add(27 * 24 * time.Hour)}}}
			output.Providers = append(output.Providers, ProviderFormatter{Name: "alibaba:" + id, Family: "alibaba", SourceID: id, Data: data})
		}
		raw, err := output.JSON(nil, now)
		if err != nil {
			t.Fatal(err)
		}
		var doc JSONOutput
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		check := func(f *JSONForecast, v *JSONAccountView) {
			t.Helper()
			if f == nil || v == nil {
				t.Fatal("legacy fields or view missing")
			}
			projection, ok := f.Windows["monthly"]
			if !ok || projection.Indicator != "unknown" || v.Reading.State != "fresh" || v.Windows[0].Forecast.State != "none" || v.Windows[0].Forecast.Pct != nil {
				t.Fatalf("unknown monthly forecast is ambiguous: %s", raw)
			}
		}
		p := doc.Providers["alibaba"]
		if p.View != nil {
			check(p.Forecast, p.View)
		}
		for _, src := range p.Sources {
			check(src.Forecast, src.View)
		}
	}
}
