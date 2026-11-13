//go:build tray

package tray

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestNextResetPollFollowsTheEarliestResetAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h", ResetsAt: now.Add(3 * time.Hour)},
			{Name: "7d", ResetsAt: now.Add(-time.Minute)},
		}},
		"codex": {Windows: []provider.UsageWindow{
			{Name: "5h", ResetsAt: now.Add(time.Hour)},
		}},
		"absent": nil,
	}
	got, ok := nextResetPoll(results, now)
	if want := now.Add(time.Hour + resetPollDelay); !ok || !got.Equal(want) {
		t.Fatalf("nextResetPoll = %v, %v; want %v, true", got, ok, want)
	}
}

// A reset that passed less than resetPollDelay ago still has its poll ahead, so
// the timer must be armed for it.
func TestNextResetPollKeepsAPollStillAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h", ResetsAt: now.Add(-10 * time.Second)},
		}},
	}
	got, ok := nextResetPoll(results, now)
	if want := now.Add(20 * time.Second); !ok || !got.Equal(want) {
		t.Fatalf("nextResetPoll = %v, %v; want %v, true", got, ok, want)
	}
}

func TestNextResetPollWithNoPollAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h"},
			{Name: "7d", ResetsAt: now.Add(-time.Minute)},
			{Name: "1d", ResetsAt: now.Add(-resetPollDelay)},
		}},
	}
	if got, ok := nextResetPoll(results, now); ok {
		t.Fatalf("nextResetPoll = %v; want none, since every reset's poll has passed", got)
	}
}

func TestDueResetSourcesNamesOnlyPollsThatHaveCome(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"early":   {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(-resetPollDelay + time.Second)}}},
		"due":     {Windows: []provider.UsageWindow{{Name: "5h"}, {Name: "7d", ResetsAt: now.Add(-resetPollDelay)}}},
		"absent":  nil,
		"unknown": {Windows: []provider.UsageWindow{{Name: "5h"}}},
	}
	got := dueResetSources(results, now)
	if len(got) != 1 || !got["due"] {
		t.Fatalf("dueResetSources = %v, want only due", got)
	}
}

// A reset poll fetches only its due sources. Another source keeps its last
// reading, and a source with no reading is still fetched.
func TestLimitToDueSourcesKeepsTheOthersReadings(t *testing.T) {
	last := map[string]*provider.UsageData{
		"due":   {Provider: "due", FetchedAt: time.Now(), Windows: []provider.UsageWindow{{Name: "5h", Utilization: 40}}},
		"other": {Provider: "other", FetchedAt: time.Now(), Windows: []provider.UsageWindow{{Name: "5h", Utilization: 20}}},
	}
	toFetch := []provider.Provider{trayStubProvider{name: "due"}, trayStubProvider{name: "other"}, trayStubProvider{name: "new"}}
	fetch, skipped := limitToDueSources(toFetch, map[string]*provider.UsageData{}, last, map[string]bool{"due": true})
	if got := providerNames(fetch); len(got) != 2 || got[0] != "due" || got[1] != "new" {
		t.Fatalf("fetch = %v, want [due new]", got)
	}
	if _, ok := skipped["due"]; ok {
		t.Fatal("a due source was kept instead of fetched")
	}
	kept := skipped["other"]
	if kept == nil || kept == last["other"] || kept.Windows[0].Utilization != 20 {
		t.Fatalf("skipped[other] = %+v, want a clone of its last reading", kept)
	}
}

// Each arm gets a new generation. The timer from an earlier arm must not run
// the reset poll once a later arm has replaced it.
func TestScheduleResetPollSupersedesEarlierArm(t *testing.T) {
	now := time.Now()
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{{Name: "5h", ResetsAt: now.Add(time.Hour)}}},
	}
	r := &usageRefresher{reloadConfig: func() { t.Error("a superseded reset poll refreshed") }}
	t.Cleanup(func() {
		if r.resetPoll != nil {
			r.resetPoll.Stop()
		}
	})
	r.scheduleResetPoll(results, now)
	first := r.resetGen
	r.scheduleResetPoll(results, now)
	if r.resetGen != first+1 || r.resetPoll == nil {
		t.Fatalf("resetGen = %d after re-arm, want %d with a poll armed", r.resetGen, first+1)
	}
	r.fireResetPoll(first)
}

// A poll whose sources are not yet due fetches nothing and re-arms the next one.
func TestResetPollWithNothingDueOnlyRearms(t *testing.T) {
	s.mu.Lock()
	saved := s.lastResults
	s.lastResults = nil
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.lastResults = saved
		s.mu.Unlock()
	})
	r := &usageRefresher{reloadConfig: func() { t.Error("a poll with nothing due fetched") }}
	r.resetGen = 1
	r.fireResetPoll(1)
	if r.resetGen != 2 || r.resetPoll != nil {
		t.Fatalf("resetGen = %d, resetPoll = %v; want re-armed with no poll ahead", r.resetGen, r.resetPoll)
	}
}
