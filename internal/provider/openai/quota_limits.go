package openai

import (
	"fmt"
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
		dst = append(dst, provider.UsageWindow{Name: name, Length: provider.WindowDuration(duration, "minute"), DisplayName: display, Utilization: used, ResetsAt: resetAt})
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

// cleanName strips control characters and surrounding space.
func cleanName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
}

// noiseTokens are vendor words that every Codex limit name repeats.
var noiseTokens = map[string]bool{"gpt": true, "codex": true, "openai": true}

// bucketLabel shortens one backend limit name from that name alone, so a
// window keeps its identity (tray selection, threshold alerts) as other
// buckets come and go: "GPT-5.3-Codex-Spark" gives "Spark 5.3" and
// "Codex-Alpha-Spark" gives "Alpha Spark".
func bucketLabel(name string) string {
	var words, versions []string
	for _, tok := range strings.FieldsFunc(cleanName(name), func(r rune) bool { return r == '-' || r == '_' || r == ' ' }) {
		switch {
		case noiseTokens[strings.ToLower(tok)]:
		case versionToken(tok) != "":
			versions = append(versions, tok)
		default:
			words = append(words, tok)
		}
	}
	label := strings.Join(append(words, versions...), " ")
	if r := []rune(label); len(r) > maxScopeLabelRunes {
		label = strings.TrimSpace(string(r[:maxScopeLabelRunes]))
	}
	if label == "" {
		label = "Extra"
	}
	if strings.EqualFold(label, "Review") {
		label += " 2" // "Review" names the code-review limit
	}
	return label
}

// resolveScopeLabels labels each bucket with bucketLabel. Two buckets whose
// names shorten identically are both kept: each gets its backend ID appended,
// so no bucket can hide another. The result has the same order as names.
func resolveScopeLabels(names, ids []string) []string {
	labels := make([]string, len(names))
	count := map[string]int{}
	for i, n := range names {
		labels[i] = bucketLabel(n)
		count[strings.ToLower(labels[i])]++
	}
	taken := map[string]bool{}
	for i := range labels {
		if count[strings.ToLower(labels[i])] > 1 {
			suffix := strconv.Itoa(i + 1)
			if i < len(ids) && strings.TrimSpace(ids[i]) != "" {
				suffix = cleanName(ids[i])
			}
			labels[i] += " " + suffix
		}
		for n := 2; taken[strings.ToLower(labels[i])]; n++ {
			labels[i] = fmt.Sprintf("%s %d", strings.TrimSuffix(labels[i], fmt.Sprintf(" %d", n-1)), n)
		}
		taken[strings.ToLower(labels[i])] = true
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
