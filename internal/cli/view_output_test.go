package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// These tests pin the machine outputs to the shared snapshot: a stale reading
// never headlines a fresh one, an exhausted window reads as out now even while
// its forecast warms up, and an unknown reset is never zero seconds.

func TestFreshHealthyWindowHeadlinesOverStaleCritical(t *testing.T) {
	now := time.Now()
	staleClaude := ProviderFormatter{Name: "claude", Display: "Claude", DisplayName: "Claude", Family: "claude", Data: &provider.UsageData{
		Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
		Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetsAt: now.Add(time.Hour)}},
	}}
	freshCodex := ProviderFormatter{Name: "openai", Display: "Codex", DisplayName: "Codex", Family: "openai", Data: &provider.UsageData{
		Provider: "openai", FetchedAt: now,
		Windows: []provider.UsageWindow{{Name: "7d", Utilization: 20, ResetsAt: now.Add(4 * 24 * time.Hour)}},
	}}
	output := &MultiProviderOutput{Providers: []ProviderFormatter{staleClaude, freshCodex}}

	agent := output.AgentSummary()
	if !strings.HasPrefix(agent, "Quota: worst=Codex 7d;") {
		t.Fatalf("agent headline = %q, want the fresh Codex window", agent)
	}
	if !strings.Contains(agent, "Claude 5h(current=100%,projected_at_reset=unknown,reset_in=") ||
		!strings.Contains(agent, ",status=stale,data=stale)") {
		t.Fatalf("agent = %q, want the stale critical window labelled stale", agent)
	}
	if strings.Contains(agent, "status=at_risk") {
		t.Fatalf("stale critical window still reports at_risk: %q", agent)
	}

	line := output.StatusLineSummary()
	if !strings.HasPrefix(line, "CM ") || !strings.Contains(line, "Codex 7d est.") || strings.Contains(line, "Claude") {
		t.Fatalf("statusline = %q, want the fresh Codex window", line)
	}
}

func TestStaleOnlyReadingIsLabelledStaleAndNeverForecast(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{
		Name: "claude", Display: "Claude", DisplayName: "Claude", Family: "claude", Data: &provider.UsageData{
			Provider: "claude", Stale: true, Warning: "rate limited", FetchedAt: now.Add(-time.Hour),
			Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetsAt: now.Add(time.Hour)}},
		},
	}}}

	if got, want := output.StatusLineSummary(), "CM~ Claude 5h last 100% stale"; got != want {
		t.Fatalf("StatusLineSummary() = %q, want %q", got, want)
	}
	agent := output.AgentSummary()
	for _, want := range []string{"status=stale", "data=stale", "projected_at_reset=unknown"} {
		if !strings.Contains(agent, want) {
			t.Fatalf("AgentSummary() = %q, missing %q", agent, want)
		}
	}
	for _, banned := range []string{"runs_out", "status=at_risk", "est."} {
		if strings.Contains(agent, banned) {
			t.Fatalf("AgentSummary() = %q, a stale reading must not contain %q", agent, banned)
		}
	}
}

func TestAgentAndStatusLineSayOutNowDuringForecastWarmUp(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{
		Name: "claude", Display: "Claude", DisplayName: "Claude", Family: "claude", Data: &provider.UsageData{
			Provider: "claude", FetchedAt: now,
			// One minute into a five-hour window: too early to project.
			Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetsAt: now.Add(299 * time.Minute)}},
		},
	}}}

	agent := output.AgentSummary()
	for _, want := range []string{"status=at_risk", "runs_out=now", "projected_at_reset=unknown"} {
		if !strings.Contains(agent, want) {
			t.Fatalf("AgentSummary() = %q, missing %q", agent, want)
		}
	}
	if strings.Contains(agent, "runs_out_in") {
		t.Fatalf("AgentSummary() = %q, warm-up has no run-out ETA", agent)
	}
	if got, want := output.StatusLineSummary(), "CM! Claude 5h current 100% projection unknown out now"; got != want {
		t.Fatalf("StatusLineSummary() = %q, want %q", got, want)
	}
}

func TestStatusLineSaysOutNowForAForecastableExhaustedWindow(t *testing.T) {
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{
		Name: "claude", Display: "Claude", DisplayName: "Claude", Family: "claude", Data: &provider.UsageData{
			Provider: "claude", FetchedAt: now,
			Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetsAt: now.Add(time.Hour)}},
		},
	}}}

	line := output.StatusLineSummary()
	if !strings.HasPrefix(line, "CM! Claude 5h est. 125% reset ") || !strings.HasSuffix(line, " out now") {
		t.Fatalf("StatusLineSummary() = %q, want an exhausted window to read out now", line)
	}
}

func TestUnknownResetIsNeverZeroSeconds(t *testing.T) {
	output := &MultiProviderOutput{Providers: []ProviderFormatter{{
		Name: "openai", Display: "Codex", DisplayName: "Codex", Family: "openai", Data: &provider.UsageData{
			Provider: "openai", FetchedAt: time.Now(),
			Windows: []provider.UsageWindow{{Name: "7d", Utilization: 40}},
		},
	}}}

	agent := output.AgentSummary()
	if !strings.Contains(agent, "reset_in=unknown") {
		t.Fatalf("AgentSummary() = %q, want reset_in=unknown", agent)
	}
	for _, banned := range []string{"reset_in=0s", "reset_in_seconds=", "reset_in=0"} {
		if strings.Contains(agent, banned) {
			t.Fatalf("AgentSummary() = %q, unknown reset must not contain %q", agent, banned)
		}
	}
	if got, want := output.StatusLineSummary(), "CM Codex 7d current 40% projection unknown"; got != want {
		t.Fatalf("StatusLineSummary() = %q, want %q", got, want)
	}
}

// viewJSON is the --json document of a small two-source Claude family plus
// one Codex account, decoded generically so absent keys are checkable.
func viewJSON(t *testing.T) map[string]any {
	t.Helper()
	now := time.Now()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{
		{
			Name: "claude", Display: "Claude · Default", DisplayName: "Claude", SourceLabel: "Default", SourceID: "default", Family: "claude", SourceCount: 2,
			Data: &provider.UsageData{Provider: "claude", SourceID: "default", FetchedAt: now, Windows: []provider.UsageWindow{
				{Name: "5h", Utilization: 30, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "7d", Utilization: 99, ResetsAt: now.Add(6 * time.Hour)},
			}},
		},
		{
			Name: "claude:odl", Display: "Claude · ODL", DisplayName: "Claude", SourceLabel: "ODL", SourceID: "odl", Family: "claude", SourceCount: 2,
			Data: &provider.UsageData{Provider: "claude", SourceID: "odl", SourceLabel: "ODL", FetchedAt: now.Add(-time.Hour), Stale: true, Warning: "rate limited",
				Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetsAt: now.Add(time.Hour)}}},
		},
		{
			Name: "openai", Display: "Codex", DisplayName: "Codex", SourceID: "default", Family: "openai", SourceCount: 1,
			Data: &provider.UsageData{Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 40},
				{Name: "5h Sonnet", Utilization: 0},
			}},
		},
	}}
	raw, err := output.JSON(nil, now)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func jpath(t *testing.T, node any, path ...any) any {
	t.Helper()
	cur := node
	for _, step := range path {
		switch key := step.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("at %v: %T is not an object", path, cur)
			}
			cur, ok = m[key]
			if !ok {
				t.Fatalf("at %v: missing %q", path, key)
			}
		case int:
			list, ok := cur.([]any)
			if !ok || key >= len(list) {
				t.Fatalf("at %v: no index %d", path, key)
			}
			cur = list[key]
		}
	}
	return cur
}

func hasKey(node any, key string) bool {
	m, ok := node.(map[string]any)
	if !ok {
		return false
	}
	_, found := m[key]
	return found
}

func TestJSONViewCarriesEveryAccountAndTheWorstTarget(t *testing.T) {
	doc := viewJSON(t)

	view := jpath(t, doc, "view").(map[string]any)
	if _, ok := view["at"].(string); !ok {
		t.Fatalf("view.at = %v, want a timestamp", view["at"])
	}
	accounts := jpath(t, doc, "view", "accounts").([]any)
	if len(accounts) != 3 || accounts[0] != "claude" || accounts[1] != "claude:odl" || accounts[2] != "openai" {
		t.Fatalf("view.accounts = %v, want the snapshot order of source keys", accounts)
	}
	if got := jpath(t, doc, "view", "worst", "account"); got != "claude" {
		t.Fatalf("view.worst.account = %v", got)
	}
	if got := jpath(t, doc, "view", "worst", "window"); got != "7d" {
		t.Fatalf("view.worst.window = %v, want the runs-out 7d window", got)
	}

	// Provider object: the v1 fields are unchanged, and the view is the default source's.
	if got := jpath(t, doc, "providers", "claude", "forecast", "windows", "5h", "projected_pct"); got == nil {
		t.Fatal("v1 projected_pct vanished")
	}
	defaultView := jpath(t, doc, "providers", "claude", "view")
	if got := jpath(t, defaultView, "reading", "state"); got != "fresh" {
		t.Fatalf("default reading state = %v", got)
	}
	win5h := jpath(t, defaultView, "windows", 0)
	if got := jpath(t, win5h, "forecast", "state"); got != "known" {
		t.Fatalf("5h forecast state = %v", got)
	}
	if got := jpath(t, win5h, "forecast", "pct"); got == nil {
		t.Fatal("known forecast must carry pct")
	}
	if got := jpath(t, win5h, "exhaustion", "state"); got != "lasts" {
		t.Fatalf("5h exhaustion = %v, want lasts", got)
	}
	if hasKey(jpath(t, win5h, "exhaustion"), "runs_out_in_seconds") {
		t.Fatal("a window that lasts must not carry a run-out ETA")
	}
	win7d := jpath(t, defaultView, "windows", 1)
	if got := jpath(t, win7d, "exhaustion", "state"); got != "runs_out" {
		t.Fatalf("7d exhaustion = %v, want runs_out", got)
	}
	if got, ok := jpath(t, win7d, "exhaustion", "runs_out_in_seconds").(float64); !ok || got <= 0 {
		t.Fatalf("runs_out_in_seconds = %v, want a positive number", got)
	}
	if !jpath(t, win7d, "reset", "known").(bool) || !hasKey(jpath(t, win7d, "reset"), "at") {
		t.Fatal("a known reset must carry its time")
	}

	// Each sources entry has its own view; the stale source keeps no forecast or run-out.
	sources := jpath(t, doc, "providers", "claude", "sources").([]any)
	var odl any
	for _, src := range sources {
		if jpath(t, src, "source", "id") == "odl" {
			odl = src
		}
	}
	if odl == nil {
		t.Fatal("odl source missing")
	}
	if got := jpath(t, odl, "view", "reading", "state"); got != "stale" {
		t.Fatalf("odl reading state = %v", got)
	}
	if got := jpath(t, odl, "view", "reading", "reason"); got == nil {
		t.Fatal("stale reading should carry its reason")
	}
	staleWin := jpath(t, odl, "view", "windows", 0)
	if got := jpath(t, staleWin, "forecast", "state"); got != "stale" || hasKey(jpath(t, staleWin, "forecast"), "pct") {
		t.Fatalf("stale forecast = %v, want state stale and no pct", jpath(t, staleWin, "forecast"))
	}
	if got := jpath(t, staleWin, "exhaustion", "state"); got != "unknown" {
		t.Fatalf("stale exhaustion = %v, want unknown", got)
	}

	// An unknown reset is absent, not zero; idle windows stay in the output but are not shown.
	codex := jpath(t, doc, "providers", "openai", "view")
	sevenDay := jpath(t, codex, "windows", 0)
	if got := jpath(t, sevenDay, "forecast", "state"); got != "none" || hasKey(jpath(t, sevenDay, "forecast"), "pct") {
		t.Fatalf("resetless forecast = %v", jpath(t, sevenDay, "forecast"))
	}
	if jpath(t, sevenDay, "reset", "known").(bool) || hasKey(jpath(t, sevenDay, "reset"), "at") {
		t.Fatalf("unknown reset must be absent: %v", jpath(t, sevenDay, "reset"))
	}
	if !jpath(t, sevenDay, "shown").(bool) {
		t.Fatal("7d window at 40% should be shown")
	}
	idle := jpath(t, codex, "windows", 1)
	if jpath(t, idle, "shown").(bool) {
		t.Fatal("an idle window must be marked not shown")
	}
}
