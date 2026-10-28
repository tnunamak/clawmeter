package provider

import (
	"strings"
	"testing"
	"time"
)

func TestLocalModelCacheDoesNotInventResetOrNewDayUsage(t *testing.T) {
	at := time.Date(2026, 10, 28, 23, 59, 0, 0, time.UTC)
	n := int64(0)
	raw := []LocalModelUsage{{Model: "new-free", Day: "2026-10-28", AssistantMessages: &n, LimitHitAt: at}}
	u := &UsageData{Provider: "opencode_free", LocalModels: raw}
	clone := u.Clone()
	*clone.LocalModels[0].AssistantMessages = 5
	if *raw[0].AssistantMessages != 0 {
		t.Fatal("clone shares mutable count")
	}
	next := LocalModelsAt(raw, at, at.Add(2*time.Minute), true)[0]
	if next.AssistantMessages != nil || !next.ResetAt.IsZero() || next.Day != "2026-10-28" {
		t.Fatalf("invented new day facts: %+v", next)
	}
	if strings.Contains(next.Summary(), "available") || !strings.Contains(next.Summary(), "reset unknown") {
		t.Fatal(next.Summary())
	}
	if LocalModelsAt(raw, at, at, false)[0].AssistantMessages != nil {
		t.Fatal("failed refresh advertised current counts")
	}
}
