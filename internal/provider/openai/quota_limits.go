package openai

import (
	"sort"
	"strconv"
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
	name = cleanName(name)
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

// cleanName strips control characters and surrounding space.
func cleanName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
}

// resolveScopeLabels turns raw limit names into short labels. A label is the
// name's last token plus its version token ("GPT-5.4-Codex-Spark" gives
// "Spark 5.4"), so it depends only on that bucket's own name and stays stable
// as other buckets come and go; tray selection and threshold alerts key on it.
// Labels never equal the reserved "Review" label. In the rare case two
// buckets still share a label, both fall back to their full cleaned name.
// The result has the same length and order as names.
func resolveScopeLabels(names []string) []string {
	const reserved = "review"
	labels := make([]string, len(names))
	count := map[string]int{}
	for i, n := range names {
		label := scopeLabel(n)
		if label == "" {
			label = "Extra"
		}
		if v := versionToken(n); v != "" && !strings.Contains(label, v) {
			label += " " + v
		}
		labels[i] = label
		count[strings.ToLower(label)]++
	}
	for i, n := range names {
		key := strings.ToLower(labels[i])
		if key == reserved {
			labels[i] += " 2"
			continue
		}
		if count[key] > 1 {
			if full := cleanName(n); full != "" && !strings.EqualFold(full, reserved) {
				labels[i] = full
			} else {
				labels[i] = labels[i] + " " + strconv.Itoa(i+1)
			}
		}
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
