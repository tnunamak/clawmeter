package openai

import (
	"fmt"
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
	for i, w := range []*codexWindow{limit.Primary, limit.Secondary} {
		used, ok := w.usable(limit.Reached)
		if !ok {
			continue
		}
		resetAt := time.Unix(w.ResetsAt, 0)
		duration := w.DurationMins
		// Older payloads omit the duration. The secondary slot is the weekly
		// window, so don't let a near reset relabel it 5h and collide with
		// the primary window.
		if i == 1 && duration <= 0 {
			duration = int64(7 * 24 * time.Hour / time.Minute)
		}
		name, display := codexWindowLabels(duration, resetAt, now)
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

// resolveScopeLabels turns raw limit names into short labels that are unique
// among themselves and never equal the reserved "Review" label, so two distinct
// buckets never share a window name. A colliding label is extended with the
// name's version token ("Spark 5.4"); if that still collides it gets a number.
// The result has the same length and order as names.
func resolveScopeLabels(names []string) []string {
	const reserved = "Review"
	shorts := make([]string, len(names))
	count := map[string]int{strings.ToLower(reserved): 1}
	for i, n := range names {
		shorts[i] = scopeLabel(n)
		if shorts[i] == "" {
			shorts[i] = "Extra"
		}
		count[strings.ToLower(shorts[i])]++
	}
	labels := make([]string, len(names))
	taken := map[string]bool{strings.ToLower(reserved): true}
	var pending []int
	for i, s := range shorts {
		if count[strings.ToLower(s)] == 1 {
			labels[i] = s
			taken[strings.ToLower(s)] = true
		} else {
			pending = append(pending, i)
		}
	}
	// Suffixes follow the backend name, not payload order, so a bucket keeps
	// its window name across polls and between the two fetch paths.
	sort.SliceStable(pending, func(a, b int) bool { return names[pending[a]] < names[pending[b]] })
	for _, i := range pending {
		candidate := shorts[i]
		if v := versionToken(names[i]); v != "" {
			candidate = shorts[i] + " " + v
		}
		for n := 2; taken[strings.ToLower(candidate)] || candidate == shorts[i]; n++ {
			candidate = fmt.Sprintf("%s %d", shorts[i], n)
		}
		labels[i] = candidate
		taken[strings.ToLower(candidate)] = true
	}
	return labels
}

// versionToken returns the first purely numeric dotted token of a name
// ("GPT-5.4-Codex-Spark" gives "5.4").
func versionToken(name string) string {
	for _, tok := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == ' ' }) {
		digits, dots := 0, 0
		for _, r := range tok {
			switch {
			case r >= '0' && r <= '9':
				digits++
			case r == '.':
				dots++
			default:
				digits = 0
				dots = -1000
			}
		}
		if digits > 0 && dots >= 0 {
			return tok
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
