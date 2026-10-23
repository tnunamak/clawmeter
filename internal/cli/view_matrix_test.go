package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray"
	"github.com/tnunamak/clawmeter/internal/view"
)

// The matrix renders each account state from one snapshot, on a frozen clock,
// through every surface that shows quota state: the tray tooltip, the tray's
// icon, its menu rows and "+N more" line, the CLI plain line, the colour table,
// the statusline, the agent line and the JSON view. The whole output of each
// case is compared with testdata/matrix_golden.txt, so a surface cannot drift
// without the golden showing it. Each case also lists words no surface may say
// for that state.
//
// Regenerate the golden after a deliberate change with:
//
//	MATRIX_UPDATE=1 go test ./internal/cli/ -run TestSurfacesAgreeFromOneSnapshot

// matrixZone is the time zone the golden file was written in. Local times on
// every surface are rendered in it, whatever the machine running the test.
var matrixZone = func() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		panic(err)
	}
	return loc
}()

// matrixNow is the frozen clock every case is built and rendered at.
var matrixNow = time.Date(2026, 10, 22, 12, 0, 0, 0, matrixZone)

const matrixGoldenPath = "testdata/matrix_golden.txt"

// matrixCase is one account state and what its surfaces must not say.
type matrixCase struct {
	name      string
	providers []ProviderFormatter
	pin       *view.Target
	// tune adjusts the snapshot inputs the tray supplies: the run-out record
	// and the extension contact.
	tune func(*view.Inputs)
	// update is the pending release version the tray tooltip shows, or "".
	update string
	// forbidden lists words that must not appear on any surface for this state.
	forbidden []string
}

// fmtr is a formatter for one source. Its key is the name and its family the family.
func fmtr(name, display, family string, data *provider.UsageData) ProviderFormatter {
	return ProviderFormatter{Name: name, Display: display, DisplayName: display, Family: family, Data: data}
}

func matrixCases(now time.Time) []matrixCase {
	claude5h := func(used float64, resetIn time.Duration) []provider.UsageWindow {
		return []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: used, ResetsAt: now.Add(resetIn)}}
	}
	return []matrixCase{
		{
			name: "fresh at 100% in warm-up",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				// One minute into a five-hour window: too early to project.
				Windows: claude5h(100, 299*time.Minute),
			})},
			forbidden: []string{"est.", "Est.", "Won't run out", "Run-out unknown"},
		},
		{
			name: "young reading kept after a 429",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-2 * time.Minute),
				Windows: claude5h(80, 3*time.Hour),
			})},
			forbidden: []string{"forecast stale", "forecast soon", "no forecast", "out now", "Out now"},
		},
		{
			name: "stale critical",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
				Windows: claude5h(100, time.Hour),
			})},
			forbidden: []string{"est.", "Est.", "out now", "Out now", "runs out", "Runs out", "Won't run out", "projected_at_reset=75"},
		},
		{
			name: "fresh healthy with a known forecast",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				// Two hours into a five-hour window at 30%: projects 75% at reset.
				Windows: claude5h(30, 3*time.Hour),
			})},
			forbidden: []string{"out now", "Out now", "runs out", "Run-out unknown", "stale"},
		},
		{
			name: "unknown reset",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 40}},
			})},
			forbidden: []string{"resets in", "Resets in", "est.", "Est.", "reset_in=0", "stale", "out now", "Out now"},
		},
		{
			name: "reset-less at 100%",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 100}},
			})},
			forbidden: []string{"resets in", "Resets in", "est.", "Est.", "Won't run out", "reset_in=0", "stale"},
		},
		{
			name: "stale reset-less window",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
				Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 40}},
			})},
			forbidden: []string{"est.", "Est.", "out now", "Out now", "runs out", "at reset", "Won't run out"},
		},
		{
			name: "reset passed",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				Windows: claude5h(40, -10*time.Minute),
			})},
			forbidden: []string{"resets in", "Resets in", "est.", "Est.", "Won't run out", "out now", "Out now", "at reset"},
		},
		{
			name: "two sources of one provider",
			providers: []ProviderFormatter{
				{Name: "claude:work", Display: "Claude · Work", DisplayName: "Claude", SourceLabel: "Work", SourceID: "work", Family: "claude", SourceCount: 2,
					Data: &provider.UsageData{Provider: "claude", SourceID: "work", FetchedAt: now, Windows: claude5h(30, 3*time.Hour)}},
				{Name: "claude:odl", Display: "Claude · ODL", DisplayName: "Claude", SourceLabel: "ODL", SourceID: "odl", Family: "claude", SourceCount: 2,
					Data: &provider.UsageData{Provider: "claude", SourceID: "odl", FetchedAt: now, Windows: claude5h(60, 2*time.Hour)}},
			},
			forbidden: []string{"Claude: 5h", "Claude 5-Hour\n"},
		},
		{
			name: "idle window hidden from list surfaces",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{
					{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 0, ResetsAt: now.Add(6 * 24 * time.Hour)},
					{Name: "5h", Length: 5 * time.Hour, Utilization: 40, ResetsAt: now.Add(3 * time.Hour)},
				},
			})},
			forbidden: []string{"7d:", "7-Day"},
		},
		{
			name: "expired with no windows",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", IsExpired: true, FetchedAt: now.Add(-time.Hour),
			})},
			forbidden: []string{"out now", "Out now", "est.", "Est.", "% used", "projected_at_reset=75"},
		},
		{
			name: "out since the tray saw it",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				Windows: claude5h(100, 2*time.Hour),
			})},
			tune: func(in *view.Inputs) {
				in.OutSince = map[string]map[string]time.Time{"claude": {"5h": now.Add(-2 * time.Hour)}}
			},
			forbidden: []string{"Won't run out", "Run-out unknown", "runs out in"},
		},
		{
			name: "reset credits, money and announcement",
			providers: []ProviderFormatter{
				fmtr("claude", "Claude", "claude", &provider.UsageData{
					Provider: "claude", FetchedAt: now,
					Windows: claude5h(30, 3*time.Hour),
					ResetCredits: &provider.UsageResetCredits{
						AvailableCount: 2,
						Credits:        []provider.UsageResetCredit{{Status: "available", ExpiresAt: now.Add(9 * 24 * time.Hour)}},
					},
					CreditPools: []provider.UsageCreditPool{provider.NewCreditPool("Org", "USD", 19988, 11,
						now.Add(10*24*time.Hour),
						[]provider.CreditGrant{{Remaining: 19988, ExpiresAt: now.Add(16 * 24 * time.Hour)}},
						[]provider.DailySpend{{Day: now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1), Amount: 100}},
						now, now)},
				}),
			},
			forbidden: []string{"expires $", "on pace to spend"},
		},
		{
			name: "announced reset",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 40, ResetsAt: now.Add(4 * 24 * time.Hour)}},
				AnnouncedReset: &provider.ResetAnnouncement{
					Feed: "codex-resets.com", SourceURL: "https://example.invalid/post",
					ScheduledFor: now.Add(4 * time.Hour), AnnouncedAt: now.Add(-time.Hour),
					FeedCheckedAt: now, ShownUntil: now.Add(24 * time.Hour),
					Windows: []string{"7d"},
				},
			})},
			forbidden: []string{"Official"},
		},
		{
			name: "update available",
			providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", &provider.UsageData{
				Provider: "claude", FetchedAt: now,
				Windows: claude5h(30, 3*time.Hour),
			})},
			update:    "0.42.0",
			forbidden: []string{"Update available: 0.41"},
		},
		{
			name: "money-only window at 100%",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{{Name: "extra", DisplayName: "Extra usage", Utilization: 100, Limit: 2000, Used: 2004, Currency: "USD"}},
			})},
			forbidden: []string{"resets", "Resets", "reset not reported", "est.", "Est.", "stale"},
		},
		{
			name: "policy-only window at 100%",
			providers: []ProviderFormatter{fmtr("openai", "Codex", "openai", &provider.UsageData{
				Provider: "openai", FetchedAt: now,
				Windows: []provider.UsageWindow{{Name: "daily", Utilization: 100, ResetPolicy: "resets daily"}},
			})},
			forbidden: []string{"reset not reported", "est.", "Est.", "Won't run out", "stale"},
		},
		{
			// The pin moves the icon and tooltip to the stale Claude window; worst=
			// and the statusline keep naming Codex, the fresh window.
			name: "pinned stale window",
			providers: []ProviderFormatter{
				fmtr("claude", "Claude", "claude", &provider.UsageData{
					Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
					Windows: claude5h(100, time.Hour),
				}),
				fmtr("codex", "Codex", "codex", &provider.UsageData{
					Provider: "openai", FetchedAt: now,
					Windows: claude5h(30, 3*time.Hour),
				}),
			},
			pin:       &view.Target{AccountKey: "claude", Window: "5h"},
			forbidden: []string{"out now", "Out now", "Est. 100%"},
		},
	}
}

// dumpCase renders one case from one snapshot and returns every surface's output.
func dumpCase(tc matrixCase) string {
	m := &MultiProviderOutput{Providers: tc.providers}
	tune := tc.tune
	if tune == nil {
		tune = func(*view.Inputs) {}
	}
	v := m.buildViewTuned(matrixNow, func(in *view.Inputs) {
		in.Pin = tc.pin
		tune(in)
	})
	data := make(map[string]*provider.UsageData, len(v.entries))
	for _, e := range v.entries {
		data[e.key] = e.pf.Data
	}

	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)

	line("tooltip: %q", tray.AppendUpdateLine(tray.Tooltip(v.snap, data), tc.update))
	providerName, icon := tray.IconState(v.snap)
	line("icon: provider=%s usage=%.0f expected=%.0f risk=%.0f show_expected=%t label=%q",
		providerName, icon.UsagePct, icon.ExpectedPct, icon.RiskPct, icon.ShowExpected, icon.Label)

	rows := tray.MenuRows(v.snap, 8)
	more := tray.MenuMore(v.snap, 8)
	for _, e := range v.entries {
		if r := rows[e.key]; len(r) > 0 {
			line("menu[%s]: %q", e.key, strings.Join(r, " | "))
		} else {
			line("menu[%s]: no rows", e.key)
		}
		if n := more[e.key]; n > 0 {
			line("menu more[%s]: %s", e.key, tray.MoreText(n))
		}
	}

	for _, l := range v.colorTable() {
		line("color: %q", ansi.ReplaceAllString(l, ""))
	}
	for i, e := range v.entries {
		line("plain[%s]: %s", e.key, v.plainLine(i))
	}
	line("statusline: %s", v.statusLine())
	line("agent: %s", v.agentLine())

	for _, e := range v.entries {
		if e.pf.Data == nil {
			continue
		}
		for _, pool := range e.pf.Data.CreditPools {
			line("money row[%s]: %s", e.key, pool.Name+": "+pool.TrayLine(matrixNow))
		}
		if a := e.pf.Data.AnnouncedReset; a != nil {
			line("announcement row[%s]: %s", e.key, a.Summary(matrixNow))
		}
	}

	doc, err := v.jsonDocument(nil, matrixNow)
	if err != nil {
		panic(err)
	}
	var out JSONOutput
	if err := json.Unmarshal(doc, &out); err != nil {
		panic(err)
	}
	families := make([]string, 0, len(out.Providers))
	for family := range out.Providers {
		families = append(families, family)
	}
	sort.Strings(families)
	for _, family := range families {
		line("json[%s]: %s", family, jsonSummary(out.Providers[family]))
	}
	return b.String()
}

// jsonSummary is the view of a provider entry, and of each of its sources.
func jsonSummary(p *ProviderJSONOutput) string {
	var parts []string
	if p.View != nil {
		parts = append(parts, accountSummary(p.View))
	}
	for _, src := range p.Sources {
		if src.View != nil {
			parts = append(parts, fmt.Sprintf("source[%s] %s", src.Source.ID, accountSummary(src.View)))
		}
	}
	if len(parts) == 0 {
		return "no view"
	}
	return strings.Join(parts, " ; ")
}

// accountSummary is one source's view: its reading, then each window's states.
func accountSummary(v *JSONAccountView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reading=%s", v.Reading.State)
	for _, w := range v.Windows {
		fmt.Fprintf(&b, " window=%s[forecast=%s exhaustion=%s reset_known=%t shown=%t", w.Name, w.Forecast.State, w.Exhaustion.State, w.Reset.Known, w.Shown)
		if w.Forecast.Pct != nil {
			fmt.Fprintf(&b, " projected_pct=%.0f", *w.Forecast.Pct)
		}
		if w.Exhaustion.RunsOutInSeconds != nil {
			fmt.Fprintf(&b, " runs_out_in_s=%d", *w.Exhaustion.RunsOutInSeconds)
		}
		if w.Exhaustion.EarlyBySeconds != nil {
			fmt.Fprintf(&b, " early_by_s=%d", *w.Exhaustion.EarlyBySeconds)
		}
		if w.Exhaustion.OutSince != "" {
			fmt.Fprintf(&b, " out_since=%s", w.Exhaustion.OutSince)
		}
		b.WriteString("]")
	}
	return b.String()
}

// goldenDumps reads the golden file: one "## name" heading per case, then the case's lines.
func goldenDumps(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(matrixGoldenPath))
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with MATRIX_UPDATE=1)", err)
	}
	out := map[string]string{}
	var name string
	var body strings.Builder
	flush := func() {
		if name != "" {
			out[name] = body.String()
		}
		body.Reset()
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "## ") {
			flush()
			name = strings.TrimPrefix(l, "## ")
			continue
		}
		if name != "" && l != "" {
			body.WriteString(l + "\n")
		}
	}
	flush()
	return out
}

func TestSurfacesAgreeFromOneSnapshot(t *testing.T) {
	local := time.Local
	time.Local = matrixZone
	t.Cleanup(func() { time.Local = local })
	cases := matrixCases(matrixNow)
	if os.Getenv("MATRIX_UPDATE") != "" {
		var b strings.Builder
		for _, tc := range cases {
			fmt.Fprintf(&b, "## %s\n%s\n", tc.name, dumpCase(tc))
		}
		if err := os.WriteFile(matrixGoldenPath, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("golden regenerated")
	}
	golden := goldenDumps(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dumpCase(tc)
			want, ok := golden[tc.name]
			if !ok {
				t.Fatalf("no golden for %q; regenerate with MATRIX_UPDATE=1", tc.name)
			}
			if got != want {
				t.Fatalf("surfaces changed for %q.\n--- want\n%s--- got\n%s", tc.name, want, got)
			}
			for _, word := range tc.forbidden {
				for _, l := range strings.Split(got, "\n") {
					if strings.Contains(l, word) {
						t.Errorf("a surface says %q, which contradicts this state: %s", word, l)
					}
				}
			}
		})
	}
}
