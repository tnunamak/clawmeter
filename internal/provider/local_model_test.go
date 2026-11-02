package provider

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"
)

// breaksLine reports runes that cannot sit in one human line: controls, and
// the line and paragraph separators U+2028 and U+2029.
func breaksLine(r rune) bool { return unicode.IsControl(r) || r == 0x2028 || r == 0x2029 }

// escapedRune is how escapeControls shows r in human text.
func escapedRune(r rune) string { return fmt.Sprintf(`\u%04x`, r) }

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
		{"bidi controls", LocalModelUsage{Model: "x-free", Label: "A" + string(rune(0x202e)) + "B" + string(rune(0x2066)) + "C" + string(rune(0x200f)) + "D", Day: "2026-10-28"}, "A" + escapedRune(0x202e) + "B" + escapedRune(0x2066) + "C" + escapedRune(0x200f) + "D: 2026-10-28 UTC · "},
		{"line separators", LocalModelUsage{Model: "x-free", Label: "A" + string(rune(0x2028)) + "B" + string(rune(0x2029)) + "C", Day: "2026-10-28"}, "A" + escapedRune(0x2028) + "B" + escapedRune(0x2029) + "C: 2026-10-28 UTC · "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.m.Summary()
			if !strings.HasPrefix(s, tc.want) {
				t.Fatalf("summary = %q, want prefix %q", s, tc.want)
			}
			if strings.ContainsFunc(s, breaksLine) {
				t.Fatalf("summary keeps a control or line separator: %q", s)
			}
		})
	}
}

func TestLocalModelSummaryBoundsLabel(t *testing.T) {
	m := LocalModelUsage{Model: "x-free", Label: strings.Repeat("m", 50), Day: "2026-10-28"}
	want := strings.Repeat("m", 37) + "...: 2026-10-28 UTC · "
	if s := m.Summary(); !strings.HasPrefix(s, want) {
		t.Fatalf("summary = %q, want prefix %q", s, want)
	}
}
