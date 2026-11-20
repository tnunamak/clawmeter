package provider

import (
	"testing"
	"time"
)

func TestObservedLocalModelNeedsRecordedUse(t *testing.T) {
	zero := int64(0)
	one := int64(1)
	limit := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		model LocalModelUsage
		want  bool
	}{
		{"catalog candidate", LocalModelUsage{Model: "m", Day: "2026-11-18"}, false},
		{"zero counts", LocalModelUsage{Model: "m", AssistantMessages: &zero, InputTokens: &zero}, false},
		{"assistant message", LocalModelUsage{Model: "m", AssistantMessages: &one}, true},
		{"cache token only", LocalModelUsage{Model: "m", CacheReadTokens: &one}, true},
		{"limit hit", LocalModelUsage{Model: "m", LimitHitAt: limit}, true},
		{"reset time alone is not use", LocalModelUsage{Model: "m", ResetAt: limit}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.model.Observed(); got != tc.want {
				t.Fatalf("Observed() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestIdleLocalCandidatesDoNotMakeProviderPresentable(t *testing.T) {
	one := int64(1)
	idle := LocalModelUsage{Model: "candidate", Day: "2026-11-18"}
	if (&UsageData{LocalModels: []LocalModelUsage{idle}}).HasPresentableUsage() {
		t.Fatal("unused catalog candidates must not make a provider presentable")
	}
	used := idle
	used.AssistantMessages = &one
	if !(&UsageData{LocalModels: []LocalModelUsage{idle, used}}).HasPresentableUsage() {
		t.Fatal("an observed model must make the provider presentable")
	}
}
