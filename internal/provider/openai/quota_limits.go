package openai

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// maxScopeLabelRunes keeps CLI rows short. Window names look like "7d Spark".
const maxScopeLabelRunes = 12

// codexWindow is one rolling quota window, independent of wire format. Both
// the app-server and the direct HTTP path convert into it so they yield the
// same Clawmeter windows.
type codexWindow struct {
	UsedPercent  *float64
	DurationMins int64
	ResetsAt     int64 // unix seconds
}

// codexLimit is one metered limit: a primary and a secondary window plus the
// backend's "limit reached" signal.
type codexLimit struct {
	Primary   *codexWindow
	Secondary *codexWindow
	// Reached is true when the backend says this limit is exhausted
	// (`limit_reached`, `allowed == false`, or a reached-type in app-server).
	Reached bool
}

func (w *codexWindow) usable(reached bool) (float64, bool) {
	if w == nil || w.ResetsAt <= 0 {
		return 0, false
	}
	if w.UsedPercent == nil {
		// Mirrors the Codex panel: an exhausted limit with no percentage is full.
		if reached {
			return 100, true
		}
		return 0, false
	}
	if *w.UsedPercent < 0 || *w.UsedPercent > 100 {
		return 0, false
	}
	return *w.UsedPercent, true
}

// appendLimitWindows adds the usable windows of one limit. scope is "" for the
// main Codex limit, otherwise a short label such as "Review". A window whose
// name is already present is skipped, so a malformed payload cannot show two
// rows with one name.
func appendLimitWindows(dst []provider.UsageWindow, scope string, limit *codexLimit, now time.Time) []provider.UsageWindow {
	if limit == nil {
		return dst
	}
	for _, w := range []*codexWindow{limit.Primary, limit.Secondary} {
		used, ok := w.usable(limit.Reached)
		if !ok {
			continue
		}
		resetAt := time.Unix(w.ResetsAt, 0)
		name, display := codexWindowLabels(w.DurationMins, resetAt, now)
		if scope != "" {
			name += " " + scope
			if display == "5h" {
				display = "5h (" + scope + ")"
			} else {
				display = "7 days (" + scope + ")"
			}
		}
		if hasWindowName(dst, name) {
			continue
		}
		dst = append(dst, provider.UsageWindow{Name: name, DisplayName: display, Utilization: used, ResetsAt: resetAt})
	}
	return dst
}

func hasWindowName(windows []provider.UsageWindow, name string) bool {
	for _, w := range windows {
		if w.Name == name {
			return true
		}
	}
	return false
}

// scopeLabel shortens a backend limit name such as "GPT-5.3-Codex-Spark" to
// its last token ("Spark") and strips control characters.
func scopeLabel(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if len([]rune(name)) > maxScopeLabelRunes {
		if i := strings.LastIndexAny(name, "- _"); i >= 0 && i+1 < len(name) {
			name = name[i+1:]
		}
	}
	if r := []rune(name); len(r) > maxScopeLabelRunes {
		name = string(r[:maxScopeLabelRunes])
	}
	return name
}

// mergeMissingWindows appends windows from extra whose names are not in data.
func mergeMissingWindows(data, extra *provider.UsageData) {
	if data == nil || extra == nil || extra.Error != "" {
		return
	}
	for _, w := range extra.Windows {
		if !hasWindowName(data.Windows, w.Name) {
			data.Windows = append(data.Windows, w)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
