package provider

import (
	"strings"
	"testing"
	"time"
	"unicode"
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

func TestLocalModelSummaryEscapesControls(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    LocalModelUsage
		want string
	}{
		{"label", LocalModelUsage{Model: "x-free", Label: "\x1b[2JFORGED\r\nROW", Day: "2026-10-28"}, `\u001b[2JFORGED\u000d\u000aROW: 2026-10-28 UTC · `},
		{"model fallback", LocalModelUsage{Model: "\x1b[2JFORGED\r\nROW-free", Day: "2026-10-28"}, `\u001b[2JFORGED\u000d\u000aROW-free: 2026-10-28 UTC · `},
		{"ordinary unicode", LocalModelUsage{Model: "x-free", Label: "Ünïcödé 模型 🦀", Day: "2026-10-28"}, "Ünïcödé 模型 🦀: 2026-10-28 UTC · "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.m.Summary()
			if !strings.HasPrefix(s, tc.want) {
				t.Fatalf("summary = %q, want prefix %q", s, tc.want)
			}
			if strings.IndexFunc(s, unicode.IsControl) >= 0 {
				t.Fatalf("summary keeps a control character: %q", s)
			}
		})
	}
}
