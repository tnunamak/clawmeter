package provider

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// LocalModelUsage is local activity, not a measured allowance or proof that a
// model can accept a request. Day is a UTC reporting day, not a reset policy.
type LocalModelUsage struct {
	Model             string    `json:"model"`
	Label             string    `json:"label"`
	Day               string    `json:"day_utc"`
	AssistantMessages *int64    `json:"assistant_messages,omitempty"`
	InputTokens       *int64    `json:"input_tokens,omitempty"`
	OutputTokens      *int64    `json:"output_tokens,omitempty"`
	ReasoningTokens   *int64    `json:"reasoning_tokens,omitempty"`
	CacheReadTokens   *int64    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens  *int64    `json:"cache_write_tokens,omitempty"`
	LimitHitAt        time.Time `json:"limit_hit_at,omitzero"`
	RetryAt           time.Time `json:"retry_hint_at,omitzero"`
	ResetAt           time.Time `json:"reset_at,omitzero"`
}

// Summary is shared by the native menu and detailed human CLI.
func (m LocalModelUsage) Summary() string {
	label := m.Label
	if label == "" {
		label = m.Model
	}
	label = displayLabel(label)
	facts := []string{m.Day + " UTC"}
	if m.AssistantMessages != nil {
		facts = append(facts, fmt.Sprintf("%d local assistant messages", *m.AssistantMessages))
	} else {
		facts = append(facts, "local usage unknown")
	}
	if m.InputTokens != nil && m.OutputTokens != nil {
		facts = append(facts, fmt.Sprintf("%d input / %d output tokens", *m.InputTokens, *m.OutputTokens))
	}
	if !m.LimitHitAt.IsZero() {
		facts = append(facts, "limit hit at "+m.LimitHitAt.UTC().Format("15:04 UTC"))
	} else {
		facts = append(facts, "no limit hit observed")
	}
	if m.ResetAt.IsZero() {
		facts = append(facts, "reset unknown")
	} else {
		facts = append(facts, "resets "+m.ResetAt.UTC().Format("Jan 2 15:04 UTC"))
	}
	return label + ": " + strings.Join(facts, " · ")
}

// escapeControls shows control characters and line or paragraph separators
// from catalog-supplied names as escapes, so they cannot clear a terminal, start
// a new row or split a one-line label. Ordinary Unicode is kept as written.
func escapeControls(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || isBidiControl(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isBidiControl reports the Unicode bidirectional formatting characters, which
// can make a label display in a different order than it is stored.
func isBidiControl(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// maxDisplayLabelRunes bounds a catalog name in one human line. It matches the
// 40-rune cap on source labels in config; JSON keeps the full name.
const maxDisplayLabelRunes = 40

// displayLabel is a catalog name as one human line: escaped, then cut to
// maxDisplayLabelRunes with "...".
func displayLabel(s string) string {
	r := []rune(escapeControls(s))
	if len(r) > maxDisplayLabelRunes {
		return string(r[:maxDisplayLabelRunes-3]) + "..."
	}
	return string(r)
}

// LocalModelsAt withholds yesterday's and failed refresh counts without turning
// the start of a new reporting day into a quota reset or an availability claim.
func LocalModelsAt(raw []LocalModelUsage, observed, at time.Time, usable bool) []LocalModelUsage {
	out := make([]LocalModelUsage, len(raw))
	copy(out, raw)
	for i := range out {
		m := &out[i]
		if !usable || observed.IsZero() || observed.After(at) || m.Day != at.UTC().Format("2006-01-02") {
			m.AssistantMessages = nil
			m.InputTokens = nil
			m.OutputTokens = nil
			m.ReasoningTokens = nil
			m.CacheReadTokens = nil
			m.CacheWriteTokens = nil
			// Preserve the dated observation; never call it today's state.
		}
	}
	return out
}
