package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray"
	"github.com/tnunamak/clawmeter/internal/view"
)

// The matrix renders each case from one snapshot, on a frozen clock, through
// every surface that shows quota state: the tray tooltip, a tray menu row, the
// CLI plain line, the agent line and the JSON view. Each case must show the
// same state on each surface, so a fact cannot be right on one and wrong on
// another.

// matrixNow is the frozen clock every case is built and rendered at.
var matrixNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)

// matrixCase is one account state, its snapshot's pin, and what each surface must say.
type matrixCase struct {
	name      string
	providers []ProviderFormatter
	pin       *view.Target
	tooltip   []string
	// rowsOf maps a source key to the words its tray menu row must carry.
	rowsOf map[string][]string
	plain  []string
	agent  []string
	// json maps a family key to the reading and window states its view must carry.
	json map[string]jsonExpect
}

// jsonExpect is what a family's view object must say about one window.
type jsonExpect struct {
	reading, window, forecast, exhaustion string
	resetKnown                            bool
}

func fmtr(name, display, family string, data *provider.UsageData) ProviderFormatter {
	return ProviderFormatter{Name: name, Display: display, DisplayName: display, Family: family, Data: data}
}

func matrixCases(now time.Time) []matrixCase {
	return []matrixCase{
		{
			name: "fresh at 100% in warm-up",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				// One minute into a five-hour window: too early to project.
				Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: now.Add(299 * time.Minute)}},
			})},
			tooltip: []string{"Claude 5-Hour", "Current 100%", "Out now", "projection unknown"},
			rowsOf:  map[string][]string{"claude": {"100% used", "forecast unknown", "out now"}},
			plain:   []string{"Claude: 5h: 100% (resets 4h59m, projection unknown · out now (4h59m until reset))"},
			agent:   []string{"worst=Claude 5h", "status=at_risk", "runs_out=now", "projected_at_reset=unknown"},
			json: map[string]jsonExpect{"claude": {
				reading: "fresh", window: "5h", forecast: "warming", exhaustion: "out", resetKnown: true,
			}},
		},
		{
			name: "stale critical",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
				Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: now.Add(time.Hour)}},
			})},
			tooltip: []string{"Claude 5-Hour", "stale - showing last good data from", "Current 100%", "Run-out unknown", "stale"},
			rowsOf:  map[string][]string{"claude": {"100% used", "forecast stale", "resets in 1h"}},
			plain:   []string{"Claude: stale (updated ", "5h: 100% (resets 1h00m, stale)"},
			agent:   []string{"worst=Claude 5h", "status=stale", "data=stale", "projected_at_reset=unknown"},
			json: map[string]jsonExpect{"claude": {
				reading: "stale", window: "5h", forecast: "stale", exhaustion: "unknown", resetKnown: true,
			}},
		},
		{
			name: "fresh healthy with a known forecast",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				// Two hours into a five-hour window at 30%: projects 75% at reset.
				Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 30, ResetsAt: now.Add(3 * time.Hour)}},
			})},
			tooltip: []string{"Claude 5-Hour", "Current 30%", "Won't run out", "Resets in 3h00m", "Est. 75% at reset"},
			rowsOf:  map[string][]string{"claude": {"30% used", "75% at reset", "resets in 3h"}},
			plain:   []string{"5h: 30% (resets 3h00m, est. 75% at reset)"},
			agent:   []string{"worst=Claude 5h", "projected_at_reset=75%", "status=on_track"},
			json: map[string]jsonExpect{"claude": {
				reading: "fresh", window: "5h", forecast: "known", exhaustion: "lasts", resetKnown: true,
			}},
		},
		{
			name: "unknown reset",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 40}},
			})},
			tooltip: []string{"Codex 7-Day", "Current 40%", "Run-out unknown", "Reset not reported"},
			rowsOf:  map[string][]string{"openai": {"40% used", "reset not reported"}},
			plain:   []string{"7d: 40% (reset not reported)"},
			agent:   []string{"worst=Codex 7d", "reset_in=unknown"},
			json: map[string]jsonExpect{"openai": {
				reading: "fresh", window: "7d", forecast: "none", exhaustion: "unknown", resetKnown: false,
			}},
		},
		{
			name: "errored",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", Error: "usage request failed (500)", FetchedAt: now.Add(-time.Hour),
				Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 60, ResetsAt: now.Add(2 * time.Hour)}},
			})},
			tooltip: []string{"Claude: "},
			rowsOf:  map[string][]string{},
			plain:   []string{"Claude: error - "},
			agent:   []string{"worst=Claude 5h", "status=stale", "data=error"},
			json: map[string]jsonExpect{"claude": {
				reading: "error", window: "5h", forecast: "stale", exhaustion: "unknown", resetKnown: true,
			}},
		},
		{
			name: "expired",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", IsExpired: true, FetchedAt: now.Add(-time.Hour),
				Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 60, ResetsAt: now.Add(2 * time.Hour)}},
			})},
			tooltip: []string{"Claude: token expired"},
			rowsOf:  map[string][]string{},
			plain:   []string{"Claude: expired"},
			agent:   []string{"worst=Claude 5h", "status=stale", "data=expired"},
			json: map[string]jsonExpect{"claude": {
				reading: "expired", window: "5h", forecast: "stale", exhaustion: "unknown", resetKnown: true,
			}},
		},
		{
			// A fresh window outranks a stale one for worst. The pin is the
			// stale Claude window, which the tooltip must show as stale anyway,
			// while worst= keeps naming Codex.
			name: "pinned target",
			providers: []ProviderFormatter{
				fmtr("claude", "Claude", "claude", &provider.UsageData{
					Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
					Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: now.Add(time.Hour)}},
				}),
				fmtr("codex", "Codex", "codex", &provider.UsageData{
					Provider: "openai", FetchedAt: now,
					Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 30, ResetsAt: now.Add(3 * time.Hour)}},
				}),
			},
			pin:     &view.Target{AccountKey: "claude", Window: "5h"},
			tooltip: []string{"Claude 5-Hour", "stale - showing last good data from", "Current 100%", "Run-out unknown"},
			rowsOf: map[string][]string{
				"claude": {"forecast stale"},
				"codex":  {"75% at reset"},
			},
			plain: []string{"Claude: stale (updated ", "Codex: 5h: 30% (resets 3h00m, est. 75% at reset)"},
			agent: []string{"worst=Codex 5h", "status=stale", "data=stale", "status=on_track"},
			json: map[string]jsonExpect{
				"claude": {reading: "stale", window: "5h", forecast: "stale", exhaustion: "unknown", resetKnown: true},
				"codex":  {reading: "fresh", window: "5h", forecast: "known", exhaustion: "lasts", resetKnown: true},
			},
		},
	}
}

// jsonViewOf decodes the view object of a family's provider entry.
func jsonViewOf(t *testing.T, out JSONOutput, family string) *JSONAccountView {
	t.Helper()
	p := out.Providers[family]
	if p == nil || p.View == nil {
		t.Fatalf("providers[%q] has no view object", family)
	}
	return p.View
}

func TestOneSnapshotStatesAgreeAcrossEverySurface(t *testing.T) {
	for _, tc := range matrixCases(matrixNow) {
		t.Run(tc.name, func(t *testing.T) {
			m := &MultiProviderOutput{Providers: tc.providers}
			// One snapshot: every surface below reads this view and nothing else.
			v := m.buildViewPinned(matrixNow, tc.pin)
			data := make(map[string]*provider.UsageData, len(v.entries))
			for _, e := range v.entries {
				data[e.key] = e.pf.Data
			}

			tooltip := tray.Tooltip(v.snap, data)
			for _, want := range tc.tooltip {
				if !strings.Contains(tooltip, want) {
					t.Errorf("tray tooltip %q missing %q", tooltip, want)
				}
			}

			rows := tray.MenuRows(v.snap, 8)
			for key, wants := range tc.rowsOf {
				row := strings.Join(rows[key], " | ")
				if len(rows[key]) == 0 {
					t.Errorf("tray menu has no row for %q", key)
				}
				for _, want := range wants {
					if !strings.Contains(row, want) {
						t.Errorf("tray menu row %q missing %q", row, want)
					}
				}
			}
			for key := range rows {
				if _, ok := tc.rowsOf[key]; !ok {
					t.Errorf("tray menu has a row for %q, want none: %q", key, rows[key])
				}
			}

			for i, want := range tc.plain {
				if i >= len(v.entries) {
					break
				}
				if got := v.plainLine(i); !strings.Contains(got, want) {
					t.Errorf("CLI plain %q missing %q", got, want)
				}
			}

			agent := v.agentLine()
			for _, want := range tc.agent {
				if !strings.Contains(agent, want) {
					t.Errorf("agent line %q missing %q", agent, want)
				}
			}

			doc, err := v.jsonDocument(nil, matrixNow)
			if err != nil {
				t.Fatalf("jsonDocument: %v", err)
			}
			var out JSONOutput
			if err := json.Unmarshal(doc, &out); err != nil {
				t.Fatalf("decode json: %v", err)
			}
			for family, want := range tc.json {
				acct := jsonViewOf(t, out, family)
				if acct.Reading.State != want.reading {
					t.Errorf("%s json reading = %q, want %q", family, acct.Reading.State, want.reading)
				}
				var w *JSONViewWindow
				for i := range acct.Windows {
					if acct.Windows[i].Name == want.window {
						w = &acct.Windows[i]
					}
				}
				if w == nil {
					t.Fatalf("%s json has no window %q", family, want.window)
				}
				if w.Forecast.State != want.forecast {
					t.Errorf("%s json forecast = %q, want %q", family, w.Forecast.State, want.forecast)
				}
				if w.Exhaustion.State != want.exhaustion {
					t.Errorf("%s json exhaustion = %q, want %q", family, w.Exhaustion.State, want.exhaustion)
				}
				if w.Reset.Known != want.resetKnown {
					t.Errorf("%s json reset known = %v, want %v", family, w.Reset.Known, want.resetKnown)
				}
			}
		})
	}
}
