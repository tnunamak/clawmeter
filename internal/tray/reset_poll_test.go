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

func TestNextResetPollWithNoResetAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	results := map[string]*provider.UsageData{
		"claude": {Windows: []provider.UsageWindow{
			{Name: "5h"},
			{Name: "7d", ResetsAt: now.Add(-time.Minute)},
			{Name: "1d", ResetsAt: now},
		}},
	}
	if got, ok := nextResetPoll(results, now); ok {
		t.Fatalf("nextResetPoll = %v; want none, since no reset is ahead of now", got)
	}
}
