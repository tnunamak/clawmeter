package cli

import (
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// summaryTiers is each provider's summary tier, in m.Providers order, read from
// one snapshot as the summary line and the check exit code read it.
func summaryTiers(m *MultiProviderOutput) []int {
	v := m.buildView(time.Now())
	tiers := make([]int, 0, len(v.entries))
	for _, e := range v.entries {
		tiers = append(tiers, summaryUrgency(e.pf, v.account(e.key)).tier)
	}
	return tiers
}

// summaryTierOf is the summary tier of one provider on its own.
func summaryTierOf(pf *ProviderFormatter) int {
	return summaryTiers(&MultiProviderOutput{Providers: []ProviderFormatter{*pf}})[0]
}

// TestSortProvidersByUrgencyFollowsTheSummaryTier checks that the --json order
// never puts a less urgent summary tier ahead of a more urgent one. Each input
// is one that the raw Stale flag grades differently from the summary: a reading
// with no observation time, a retained healthy reading, a passed reset and a
// retained full window.
func TestSortProvidersByUrgencyFollowsTheSummaryTier(t *testing.T) {
	now := time.Now()
	window := func(used float64, resetsIn time.Duration) []provider.UsageWindow {
		return []provider.UsageWindow{{Name: "5h", Utilization: used, ResetsAt: now.Add(resetsIn)}}
	}
	retained := func(used float64) *provider.UsageData {
		d := &provider.UsageData{FetchedAt: now, Windows: window(used, 30*time.Minute)}
		d.MarkStale("cache expired")
		return d
	}
	out := &MultiProviderOutput{Providers: []ProviderFormatter{
		// No observation time: the view reads the 100% window as stale, a warning.
		// Its window projects higher than the fresh one's, so a tier-2 tie
		// breaks in its favour on the raw flag.
		{Name: "unstamped", Data: &provider.UsageData{Windows: window(100, 30*time.Minute)}},
		{Name: "retained-healthy", Data: retained(10)},
		{Name: "fresh-critical", Data: &provider.UsageData{FetchedAt: now, Windows: window(100, 10*time.Minute)}},
		{Name: "passed-reset", Data: &provider.UsageData{FetchedAt: now, Windows: window(10, -time.Minute)}},
		{Name: "retained-full", Data: retained(100)},
	}}
	if got, want := summaryTiers(out), []int{3, 4, 2, 3, 3}; !slices.Equal(got, want) {
		t.Fatalf("fixture summary tiers = %v, want %v", got, want)
	}

	sortProvidersByUrgency(out)

	if after := summaryTiers(out); !slices.IsSorted(after) {
		t.Fatalf("summary tiers after the sort = %v, want the most urgent first", after)
	}
	if got := statusCheckExitCode(out); got != 2 {
		t.Fatalf("exit code = %d, want 2 for the fresh critical window", got)
	}
}

// TestJSONDocumentDoesNotDependOnProviderOrder checks what makes the urgency
// order invisible: the --json providers object is a map, and each family's
// sources are listed by source ID. Shuffling the providers, and sorting them
// as the status path does, must leave the document byte-for-byte unchanged.
func TestJSONDocumentDoesNotDependOnProviderOrder(t *testing.T) {
	now := time.Now()
	window := func(used float64, resetsIn time.Duration) []provider.UsageWindow {
		return []provider.UsageWindow{{Name: "5h", Utilization: used, ResetsAt: now.Add(resetsIn)}}
	}
	retained := func(used float64) *provider.UsageData {
		d := &provider.UsageData{FetchedAt: now, Windows: window(used, 30*time.Minute)}
		d.MarkStale("cache expired")
		return d
	}
	providers := []ProviderFormatter{
		{Name: "unstamped", Family: "unstamped", SourceID: "default", Data: &provider.UsageData{Windows: window(100, 30*time.Minute)}},
		{Name: "retained-healthy", Family: "retained", SourceID: "default", Data: retained(10)},
		{Name: "fresh-critical", Family: "fresh", SourceID: "default", Data: &provider.UsageData{FetchedAt: now, Windows: window(100, 10*time.Minute)}},
		{Name: "claude-a", Family: "claude", SourceID: "a", Data: &provider.UsageData{FetchedAt: now, Windows: window(20, time.Hour)}},
		{Name: "claude-b", Family: "claude", SourceID: "b", Data: &provider.UsageData{FetchedAt: now, Windows: window(95, 30*time.Minute)}},
		{Name: "passed-reset", Family: "passed", SourceID: "default", Data: &provider.UsageData{FetchedAt: now, Windows: window(10, -time.Minute)}},
	}
	document := func(ps []ProviderFormatter) string {
		b, err := (&MultiProviderOutput{Providers: ps}).JSON(nil, now)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	want := document(providers)
	rng := rand.New(rand.NewSource(1))
	for i := range 50 {
		shuffled := slices.Clone(providers)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := document(shuffled); got != want {
			t.Fatalf("shuffle %d changed the JSON document", i)
		}
		out := &MultiProviderOutput{Providers: shuffled}
		sortProvidersByUrgency(out)
		if got := document(out.Providers); got != want {
			t.Fatalf("sorting shuffle %d changed the JSON document", i)
		}
	}
}
