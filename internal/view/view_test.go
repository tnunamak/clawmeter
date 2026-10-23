package view

import (
	"reflect"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// at is the frozen clock every test builds against.
var at = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// Reset-backed five-hour windows. A reset 3h away is 2h into the window
// (40%): far enough to project. A reset 4h45m away is 15m in: still warming.
const (
	midWindow  = 3 * time.Hour
	warmWindow = 4*time.Hour + 45*time.Minute
)

func fiveHour(used float64, resetIn time.Duration) provider.UsageWindow {
	return provider.UsageWindow{Name: "5h", Length: forecast.FiveHourWindow, Utilization: used, ResetsAt: at.Add(resetIn)}
}

func namedWindow(name string, used float64, resetIn time.Duration) provider.UsageWindow {
	w := fiveHour(used, resetIn)
	w.Name = name
	return w
}

// fresh is a successful reading fetched a minute ago.
func fresh(name string, windows ...provider.UsageWindow) *provider.UsageData {
	return &provider.UsageData{Provider: name, FetchedAt: at.Add(-time.Minute), Windows: windows}
}

func inputs(results map[string]*provider.UsageData) Inputs {
	return Inputs{Results: results}
}

func account(t *testing.T, snap Snapshot, key string) Account {
	t.Helper()
	for _, a := range snap.Accounts {
		if a.Key == key {
			return a
		}
	}
	t.Fatalf("no account %q in %+v", key, snap.Accounts)
	return Account{}
}

func findWindow(t *testing.T, a Account, name string) Window {
	t.Helper()
	for _, w := range a.Windows {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("no window %q in account %q", name, a.Key)
	return Window{}
}

func TestReadingStates(t *testing.T) {
	errored := fresh("claude", fiveHour(20, midWindow))
	errored.Error = "connection refused"
	expired := fresh("claude", fiveHour(20, midWindow))
	expired.IsExpired = true
	stale := fresh("claude", fiveHour(20, midWindow))
	stale.Stale = true
	stale.Warning = "rate limited"

	tests := []struct {
		name   string
		data   *provider.UsageData
		state  ReadingState
		reason string
	}{
		{"fresh", fresh("claude", fiveHour(20, midWindow)), ReadingFresh, ""},
		{"error", errored, ReadingError, format.HumanizeError("connection refused")},
		{"expired", expired, ReadingExpired, ""},
		{"stale", stale, ReadingStale, format.HumanizeError("rate limited")},
		{"stale without warning", &provider.UsageData{Provider: "claude", Stale: true}, ReadingStale, "usage unavailable"},
		{"unavailable", nil, ReadingUnavailable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := Build(inputs(map[string]*provider.UsageData{"claude": tt.data}), at)
			got := account(t, snap, "claude").Reading
			if got.State != tt.state {
				t.Fatalf("state = %q, want %q", got.State, tt.state)
			}
			if got.Reason != tt.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tt.reason)
			}
			if tt.data != nil && !got.ObservedAt.Equal(tt.data.FetchedAt) {
				t.Fatalf("observed at = %v, want %v", got.ObservedAt, tt.data.FetchedAt)
			}
		})
	}
}

func TestNotFreshReadingEstablishesNothing(t *testing.T) {
	// A stale reading at 100% keeps its windows but claims no exhaustion and
	// no forecast, even where a fresh one would.
	data := fresh("claude", fiveHour(100, midWindow), namedWindow("7d", 30, midWindow))
	data.Stale = true
	snap := Build(inputs(map[string]*provider.UsageData{"claude": data}), at)
	a := account(t, snap, "claude")
	for _, w := range a.Windows {
		if w.Exhaustion.State != ExhaustionUnknown {
			t.Errorf("%s exhaustion = %q, want unknown", w.Name, w.Exhaustion.State)
		}
		if w.Forecast.State != ForecastStale {
			t.Errorf("%s forecast = %q, want stale", w.Name, w.Forecast.State)
		}
	}
	if len(a.Windows) != 2 {
		t.Fatalf("windows = %d, want all 2 kept", len(a.Windows))
	}
}

func TestExhaustionAndForecast(t *testing.T) {
	tests := []struct {
		name     string
		window   provider.UsageWindow
		forecast ForecastState
		exhaust  ExhaustionState
	}{
		{"lasts with forecast", fiveHour(20, midWindow), ForecastKnown, ExhaustionLasts},
		{"runs out before reset", fiveHour(80, midWindow), ForecastKnown, ExhaustionRunsOut},
		{"out at 100 during warm-up", fiveHour(100, warmWindow), ForecastWarming, ExhaustionOut},
		{"out at 100 with forecast", fiveHour(100, midWindow), ForecastKnown, ExhaustionOut},
		{"warming below 100", fiveHour(10, warmWindow), ForecastWarming, ExhaustionUnknown},
		{"no reset", provider.UsageWindow{Name: "5h", Length: forecast.FiveHourWindow, Utilization: 20}, ForecastNone, ExhaustionUnknown},
		{"no reset and out", provider.UsageWindow{Name: "5h", Length: forecast.FiveHourWindow, Utilization: 100}, ForecastNone, ExhaustionOut},
		{"reset already passed", fiveHour(50, -time.Minute), ForecastStale, ExhaustionUnknown},
		{"no known window length", provider.UsageWindow{Name: "usage", Utilization: 20, ResetsAt: at.Add(midWindow)}, ForecastNone, ExhaustionUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude", tt.window)}), at)
			w := findWindow(t, account(t, snap, "claude"), tt.window.Name)
			if w.Forecast.State != tt.forecast {
				t.Errorf("forecast = %q, want %q", w.Forecast.State, tt.forecast)
			}
			if w.Exhaustion.State != tt.exhaust {
				t.Errorf("exhaustion = %q, want %q", w.Exhaustion.State, tt.exhaust)
			}
		})
	}
}

func TestForecastValuesAndRunOutTiming(t *testing.T) {
	// 20% in 2h of a 5h window projects to 50% at reset and lasts.
	lasts := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude", fiveHour(20, midWindow))}), at)
	w := findWindow(t, account(t, lasts, "claude"), "5h")
	if w.Forecast.Pct < 49.9 || w.Forecast.Pct > 50.1 {
		t.Errorf("projected = %v, want about 50", w.Forecast.Pct)
	}
	if w.Exhaustion.RunsOutIn != 0 || w.Exhaustion.BlockedFor != 0 {
		t.Errorf("lasts carries run-out times: %+v", w.Exhaustion)
	}

	// 80% in 2h runs out 30m from now, 2h30m before the reset.
	runs := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude", fiveHour(80, midWindow))}), at)
	w = findWindow(t, account(t, runs, "claude"), "5h")
	if w.Exhaustion.RunsOutIn != 30*time.Minute {
		t.Errorf("runs out in %v, want 30m", w.Exhaustion.RunsOutIn)
	}
	if w.Exhaustion.BlockedFor != 2*time.Hour+30*time.Minute {
		t.Errorf("early by %v, want 2h30m", w.Exhaustion.BlockedFor)
	}
}

func TestResetStates(t *testing.T) {
	policy := provider.UsageWindow{Name: "weekly", Length: forecast.SevenDayWindow, Utilization: 5, ResetPolicy: "resets with the plan"}
	none := provider.UsageWindow{Name: "bare", Length: forecast.FiveHourWindow, Utilization: 5}
	snap := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude", fiveHour(5, midWindow), policy, none)}), at)
	a := account(t, snap, "claude")

	if r := findWindow(t, a, "5h").Reset; !r.Known || !r.At.Equal(at.Add(midWindow)) || r.Policy != "" {
		t.Errorf("timed reset = %+v", r)
	}
	if r := findWindow(t, a, "weekly").Reset; r.Known || r.Policy != "resets with the plan" || !r.At.IsZero() {
		t.Errorf("policy-only reset = %+v", r)
	}
	if r := findWindow(t, a, "bare").Reset; r.Known || !r.At.IsZero() || r.Policy != "" {
		t.Errorf("unknown reset = %+v, want zero", r)
	}
}

func TestLabels(t *testing.T) {
	odl := fresh("claude", fiveHour(10, midWindow))
	main := fresh("claude", fiveHour(10, midWindow))
	in := Inputs{
		Results:            map[string]*provider.UsageData{"claude": main, "claude:odl": odl},
		DisplayNames:       map[string]string{"claude": "Claude", "claude:odl": "Claude"},
		SourceLabels:       map[string]string{"claude": "Dondo", "claude:odl": "ODL"},
		Families:           map[string]string{"claude": "claude", "claude:odl": "claude"},
		FamilySourceCounts: map[string]int{"claude": 2},
	}
	snap := Build(in, at)
	if got := account(t, snap, "claude").Label; got != "Claude · Dondo" {
		t.Errorf("multi-source label = %q", got)
	}
	if got := account(t, snap, "claude:odl").Label; got != "Claude · ODL" {
		t.Errorf("multi-source label = %q", got)
	}

	in.FamilySourceCounts = map[string]int{"claude": 1}
	snap = Build(in, at)
	if got := account(t, snap, "claude").Label; got != "Claude" {
		t.Errorf("single-source family label = %q, want bare display name", got)
	}
}

func TestLabelFallsBackToFamilyFromData(t *testing.T) {
	// Without a Families entry the data's provider name is the family.
	in := Inputs{
		Results:            map[string]*provider.UsageData{"claude": fresh("claude")},
		DisplayNames:       map[string]string{"claude": "Claude"},
		SourceLabels:       map[string]string{"claude": "Dondo"},
		FamilySourceCounts: map[string]int{"claude": 2},
	}
	if got := account(t, Build(in, at), "claude").Label; got != "Claude · Dondo" {
		t.Errorf("label = %q", got)
	}
}

func TestAccountOrder(t *testing.T) {
	in := Inputs{
		Results: map[string]*provider.UsageData{
			"zeta":    fresh("zeta"),
			"b-key":   fresh("beta"),
			"a-key":   fresh("alpha"),
			"Alpha-2": fresh("alpha"),
			"nil-src": nil,
		},
		DisplayNames: map[string]string{"zeta": "zeta", "b-key": "Beta", "a-key": "alpha", "Alpha-2": "ALPHA", "nil-src": "Nil"},
	}
	var got []string
	for _, a := range Build(in, at).Accounts {
		got = append(got, a.Key)
	}
	// Labels compare case-insensitively: alpha, ALPHA, Beta, Nil, zeta. Equal
	// labels fall back to the key, byte order, so "Alpha-2" sorts first.
	want := []string{"Alpha-2", "a-key", "b-key", "nil-src", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestShownDropsIdleWindows(t *testing.T) {
	idle := fiveHour(0, midWindow)
	idle.Name = "idle"
	busy := namedWindow("busy", 10, midWindow)
	warming := namedWindow("warming", 0, warmWindow)
	snap := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude", idle, busy, warming)}), at)
	a := account(t, snap, "claude")
	if len(a.Windows) != 3 {
		t.Fatalf("windows = %d, want every window kept", len(a.Windows))
	}
	if want := []string{"busy"}; !reflect.DeepEqual(a.Shown, want) {
		t.Fatalf("shown = %v, want %v", a.Shown, want)
	}
}

func TestShownAllIdleShowsEverything(t *testing.T) {
	a := namedWindow("a", 0, midWindow)
	b := namedWindow("b", 0, warmWindow)
	snap := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude", a, b)}), at)
	if want := []string{"a", "b"}; !reflect.DeepEqual(account(t, snap, "claude").Shown, want) {
		t.Fatalf("shown = %v, want %v", account(t, snap, "claude").Shown, want)
	}
}

func TestWorstRanksClassesFirst(t *testing.T) {
	// A stale account at 100% must not beat a fresh window with a known reset.
	stale := fresh("claude", namedWindow("5h", 100, midWindow))
	stale.Stale = true
	fine := fresh("codex", namedWindow("5h", 10, midWindow))
	noReset := fresh("gemini", provider.UsageWindow{Name: "daily", Utilization: 100})
	snap := Build(inputs(map[string]*provider.UsageData{"claude": stale, "codex": fine, "gemini": noReset}), at)
	if snap.Worst == nil || snap.Worst.AccountKey != "codex" {
		t.Fatalf("worst = %+v, want the fresh codex window", snap.Worst)
	}
	if got := findWindow(t, account(t, snap, "claude"), "5h").Risk; got == 1 {
		t.Errorf("stale window ranked first")
	}
}

func TestWorstTieBreaks(t *testing.T) {
	tests := []struct {
		name    string
		results map[string]*provider.UsageData
		want    string
	}{
		{
			name: "out beats runs out",
			results: map[string]*provider.UsageData{
				"a": fresh("a", namedWindow("w", 80, midWindow)),
				"b": fresh("b", namedWindow("w", 100, midWindow)),
			},
			want: "b",
		},
		{
			name: "runs out soonest first",
			results: map[string]*provider.UsageData{
				"a": fresh("a", namedWindow("w", 70, midWindow)),
				"b": fresh("b", namedWindow("w", 80, midWindow)),
			},
			want: "b",
		},
		{
			name: "known forecast beats unknown",
			results: map[string]*provider.UsageData{
				"a": fresh("a", namedWindow("w", 10, midWindow)),
				"b": fresh("b", namedWindow("w", 40, warmWindow)),
			},
			want: "a",
		},
		{
			// a: 15% after 1h projects to 75%; b: 20% after 2h projects to 50%.
			// b uses more now, but a is on pace for more by reset.
			name: "higher forecast before higher use",
			results: map[string]*provider.UsageData{
				"a": fresh("a", namedWindow("w", 15, 4*time.Hour)),
				"b": fresh("b", namedWindow("w", 20, midWindow)),
			},
			want: "a",
		},
		{
			name: "account order breaks an exact tie",
			results: map[string]*provider.UsageData{
				"b": fresh("b", namedWindow("w", 20, midWindow)),
				"a": fresh("a", namedWindow("w", 20, midWindow)),
			},
			want: "a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := Build(inputs(tt.results), at)
			if snap.Worst == nil || snap.Worst.AccountKey != tt.want {
				t.Fatalf("worst = %+v, want account %q", snap.Worst, tt.want)
			}
		})
	}
}

func TestRiskIsAUniqueRankAcrossAccounts(t *testing.T) {
	snap := Build(inputs(map[string]*provider.UsageData{
		"a": fresh("a", namedWindow("5h", 10, midWindow), namedWindow("7d", 90, midWindow)),
		"b": fresh("b", namedWindow("5h", 100, midWindow)),
	}), at)
	seen := map[int]bool{}
	var risks []int
	for _, acc := range snap.Accounts {
		for _, w := range acc.Windows {
			if seen[w.Risk] {
				t.Fatalf("risk %d repeated", w.Risk)
			}
			seen[w.Risk] = true
			risks = append(risks, w.Risk)
		}
	}
	if len(risks) != 3 {
		t.Fatalf("risks = %v", risks)
	}
	if got := findWindow(t, account(t, snap, "b"), "5h").Risk; got != 1 {
		t.Errorf("out window risk = %d, want 1", got)
	}
}

func TestSelectedPinAndWorst(t *testing.T) {
	results := map[string]*provider.UsageData{
		"claude": fresh("claude", namedWindow("5h", 100, midWindow)),
		"codex":  fresh("codex", namedWindow("5h", 10, midWindow), namedWindow("7d", 5, midWindow)),
	}

	noPin := Build(inputs(results), at)
	if noPin.Selected == nil || *noPin.Selected != *noPin.Worst {
		t.Fatalf("no pin: selected = %+v, want worst %+v", noPin.Selected, noPin.Worst)
	}

	pinned := inputs(results)
	pinned.Pin = &Target{AccountKey: "codex", Window: "7d"}
	snap := Build(pinned, at)
	if snap.Selected == nil || snap.Selected.AccountKey != "codex" || snap.Selected.Window != "7d" || snap.Selected.Reason != "pinned" {
		t.Fatalf("valid pin: selected = %+v", snap.Selected)
	}
	if snap.Worst == nil || snap.Worst.AccountKey != "claude" {
		t.Fatalf("pin must not move worst: %+v", snap.Worst)
	}

	missing := inputs(results)
	missing.Pin = &Target{AccountKey: "codex", Window: "monthly"}
	snap = Build(missing, at)
	if snap.Selected == nil || *snap.Selected != *snap.Worst {
		t.Fatalf("pin to missing window: selected = %+v, want worst %+v", snap.Selected, snap.Worst)
	}
}

func TestOutSincePassesThroughOnlyWhenOut(t *testing.T) {
	since := at.Add(-40 * time.Minute)
	in := inputs(map[string]*provider.UsageData{
		"claude": fresh("claude", namedWindow("5h", 100, midWindow), namedWindow("7d", 100, midWindow)),
	})
	in.OutSince = map[string]map[string]time.Time{"claude": {"5h": since, "7d": since}}
	snap := Build(in, at)
	a := account(t, snap, "claude")
	if got := findWindow(t, a, "5h").Exhaustion.OutSince; !got.Equal(since) {
		t.Errorf("out since = %v, want %v", got, since)
	}

	// Under 100% the record does not apply, so it is not passed on.
	low := inputs(map[string]*provider.UsageData{"claude": fresh("claude", namedWindow("5h", 50, midWindow))})
	low.OutSince = map[string]map[string]time.Time{"claude": {"5h": since}}
	if got := findWindow(t, account(t, Build(low, at), "claude"), "5h").Exhaustion.OutSince; !got.IsZero() {
		t.Errorf("out since on a window under 100%% = %v, want zero", got)
	}
}

func TestPoolActionIsEvaluatedAtSnapshotTime(t *testing.T) {
	const signIn = "sign in to the Claude Console"
	const update = "open your browser to update"
	observed := at.Add(-10 * time.Minute)
	data := fresh("claude_api")
	data.CreditPools = []provider.UsageCreditPool{{
		Name:              "Org",
		Currency:          "USD",
		ObservationSource: "extension",
		ObservedAt:        observed,
	}}

	// Extension reached Clawmeter 1 minute before the snapshot: a signed-in
	// browser that cannot read the console, so sign in is the step.
	in := inputs(map[string]*provider.UsageData{"claude_api": data})
	in.ExtensionContact = at.Add(-time.Minute)
	pool := account(t, Build(in, at), "claude_api").Pools[0]
	if got := pool.Action(at); got != signIn {
		t.Errorf("action = %q, want %q", got, signIn)
	}

	// The same contact, seen 10 minutes later, is too old: update the browser.
	if got := pool.Action(at.Add(10 * time.Minute)); got != update {
		t.Errorf("later action = %q, want %q", got, update)
	}

	// No contact at all: update the browser.
	none := Build(inputs(map[string]*provider.UsageData{"claude_api": data}), at)
	if got := account(t, none, "claude_api").Pools[0].Action(at); got != update {
		t.Errorf("no contact action = %q, want %q", got, update)
	}
}

func TestResetCreditsEffective(t *testing.T) {
	data := fresh("claude",
		fiveHour(10, midWindow),
	)
	data.ResetCredits = &provider.UsageResetCredits{
		Snapshot:  true,
		FetchedAt: at.Add(-10 * time.Minute),
		Credits: []provider.UsageResetCredit{
			{Status: "available", ExpiresAt: at.Add(time.Hour)},
			{Status: "available", ExpiresAt: at.Add(-time.Hour)},
			{Status: "consumed", ExpiresAt: at.Add(2 * time.Hour), ConsumedAt: at.Add(-time.Minute)},
		},
	}
	credits := account(t, Build(inputs(map[string]*provider.UsageData{"claude": data}), at), "claude").ResetCredits
	if credits == nil {
		t.Fatal("no credits")
	}
	if credits.Count != 1 {
		t.Errorf("count = %d, want 1 usable now", credits.Count)
	}
	if !credits.NextExpiry.Equal(at.Add(time.Hour)) {
		t.Errorf("next expiry = %v", credits.NextExpiry)
	}
	if !credits.Snapshot || !credits.Stale {
		t.Errorf("snapshot=%v stale=%v, want both true for a 10 minute old snapshot", credits.Snapshot, credits.Stale)
	}

	none := Build(inputs(map[string]*provider.UsageData{"claude": fresh("claude")}), at)
	if got := account(t, none, "claude").ResetCredits; got != nil {
		t.Errorf("credits = %+v, want nil without an inventory", got)
	}
}

func TestAnnouncementPassesThrough(t *testing.T) {
	data := fresh("codex", namedWindow("7d", 10, midWindow))
	data.AnnouncedReset = &provider.ResetAnnouncement{Feed: provider.ResetAnnouncementFeed, Windows: []string{"7d"}}
	snap := Build(inputs(map[string]*provider.UsageData{"codex": data}), at)
	got := account(t, snap, "codex").Announcement
	if got == nil || got.Feed != provider.ResetAnnouncementFeed || !reflect.DeepEqual(got.Windows, []string{"7d"}) {
		t.Fatalf("announcement = %+v", got)
	}
}

func TestBuildDoesNotMutateInputs(t *testing.T) {
	makeInputs := func() Inputs {
		data := fresh("claude",
			fiveHour(100, midWindow),
			namedWindow("7d", 0, warmWindow),
		)
		data.AnnouncedReset = &provider.ResetAnnouncement{Feed: "f", Windows: []string{"5h"}}
		data.CreditPools = []provider.UsageCreditPool{{Name: "p", ObservationSource: "extension", ObservedAt: at.Add(-time.Minute)}}
		data.ResetCredits = &provider.UsageResetCredits{Credits: []provider.UsageResetCredit{{Status: "available", ExpiresAt: at.Add(time.Hour)}}}
		return Inputs{
			Results:            map[string]*provider.UsageData{"claude": data, "nil": nil},
			DisplayNames:       map[string]string{"claude": "Claude"},
			SourceLabels:       map[string]string{"claude": "Dondo"},
			FamilySourceCounts: map[string]int{"claude": 2},
			OutSince:           map[string]map[string]time.Time{"claude": {"5h": at.Add(-time.Hour)}},
			ExtensionContact:   at.Add(-time.Minute),
			Pin:                &Target{AccountKey: "claude", Window: "5h"},
		}
	}
	in := makeInputs()
	Build(in, at)
	if !reflect.DeepEqual(in, makeInputs()) {
		t.Fatal("Build changed its inputs")
	}
}

func TestSnapshotCarriesItsClock(t *testing.T) {
	snap := Build(inputs(nil), at)
	if !snap.At.Equal(at) || len(snap.Accounts) != 0 || snap.Worst != nil || snap.Selected != nil {
		t.Fatalf("empty snapshot = %+v", snap)
	}
}

func TestAFullWindowWhoseResetHasPassedIsNotOut(t *testing.T) {
	snap := Build(inputs(map[string]*provider.UsageData{"codex": fresh("openai", fiveHour(100, -time.Minute))}), at)
	if got := findWindow(t, account(t, snap, "codex"), "5h").Exhaustion.State; got != ExhaustionUnknown {
		t.Fatalf("exhaustion = %v; a reading from before the reset says nothing about now", got)
	}
}

func TestASourceWithoutALabelIsNamedByItsID(t *testing.T) {
	data := fresh("claude", fiveHour(10, midWindow))
	data.SourceID = "work"
	in := Inputs{
		Results:            map[string]*provider.UsageData{"claude:work": data},
		DisplayNames:       map[string]string{"claude:work": "Claude"},
		Families:           map[string]string{"claude:work": "claude"},
		FamilySourceCounts: map[string]int{"claude": 2},
	}
	if got := account(t, Build(in, at), "claude:work").Label; got != "Claude · work" {
		t.Fatalf("label = %q", got)
	}
}

func TestANotFreshReadingWithoutAResetIsStale(t *testing.T) {
	for _, mark := range []func(*provider.UsageData){
		func(d *provider.UsageData) { d.Stale = true },
		func(d *provider.UsageData) { d.Error = "authentication failed" },
		func(d *provider.UsageData) { d.IsExpired = true },
	} {
		data := fresh("zai", provider.UsageWindow{Name: "daily", Utilization: 20})
		mark(data)
		w := findWindow(t, account(t, Build(inputs(map[string]*provider.UsageData{"zai": data}), at), "zai"), "daily")
		if w.Forecast.State != ForecastStale || w.Exhaustion.State != ExhaustionUnknown {
			t.Fatalf("forecast %v, exhaustion %v; want stale and unknown", w.Forecast.State, w.Exhaustion.State)
		}
	}
}

func TestEqualForecastsRankByCurrentUse(t *testing.T) {
	snap := Build(inputs(map[string]*provider.UsageData{
		"a": fresh("a", fiveHour(10, 4*time.Hour)),
		"b": fresh("b", fiveHour(20, 3*time.Hour)),
	}), at)
	if snap.Worst == nil || snap.Worst.AccountKey != "b" {
		t.Fatalf("worst = %+v; with equal forecasts the higher current use ranks first", snap.Worst)
	}
}
