//go:build tray

package tray

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// recoveryWatch finds accounts that ran out and now have room. It lives in
// memory only: a tray that starts, or an account read under a new identity,
// sets a baseline, so an old episode is never replayed.
type recoveryWatch struct {
	mu       sync.Mutex
	episodes map[string]blockedEpisode
}

// blockedEpisode is one account's time out of quota: the windows seen full,
// and when the account was last read.
type blockedEpisode struct {
	account  string
	windows  map[string]bool
	lastSeen time.Time
}

// observe records this refresh's fresh readings and returns the sources that
// recovered: the previous reading of the same account, at most maxGap
// earlier, had a full window, and this one shows every window that was full
// below 100%. A window missing from the reading leaves the episode open, and
// cached, stale, errored or expired readings are ignored.
func (r *recoveryWatch) observe(results, cached map[string]*provider.UsageData, identities map[string]string, now time.Time, maxGap time.Duration) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.episodes == nil {
		r.episodes = make(map[string]blockedEpisode)
	}
	var recovered []string
	for source, data := range results {
		account := identities[source]
		if _, wasCached := cached[source]; wasCached || account == "" || !freshReading(data) {
			continue
		}
		episode, open := r.episodes[source]
		if open && (episode.account != account || now.Sub(episode.lastSeen) > maxGap || !now.After(episode.lastSeen)) {
			delete(r.episodes, source)
			open = false
		}
		full := make(map[string]bool)
		present := make(map[string]bool)
		for _, w := range data.Windows {
			present[w.Name] = true
			if w.Utilization >= 100 {
				full[w.Name] = true
			}
		}
		if len(full) > 0 {
			if !open {
				episode = blockedEpisode{account: account, windows: make(map[string]bool)}
			}
			for name := range full {
				episode.windows[name] = true
			}
			episode.lastSeen = now
			r.episodes[source] = episode
			continue
		}
		if !open {
			continue
		}
		complete := true
		for name := range episode.windows {
			if !present[name] {
				complete = false
			}
		}
		if !complete {
			episode.lastSeen = now
			r.episodes[source] = episode
			continue
		}
		delete(r.episodes, source)
		recovered = append(recovered, source)
	}
	sort.Strings(recovered)
	return recovered
}

// quotaAvailableNotice words the notification for accounts the tray saw run
// out that now have quota in every window. It states the readings and when
// they were taken, nothing more: quota on the meter is not proof that a
// request will succeed, and no advice is given.
func quotaAvailableNotice(sources []string, results map[string]*provider.UsageData, menus map[string]*providerMenuItems, now time.Time) (title, body string) {
	var lines []string
	var labels []string
	for _, source := range sources {
		data := results[source]
		if data == nil {
			continue
		}
		label := source
		if menu := menus[source]; menu != nil && menu.displayName != "" {
			label = menu.displayName
		}
		var used []string
		for _, w := range data.Windows {
			used = append(used, meter.Code(w)+" "+meter.Percent(w.Utilization)+" used")
		}
		line := label
		if len(used) > 0 {
			line += ": " + strings.Join(used, ", ")
		}
		labels = append(labels, label)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", ""
	}
	title = "Quota available again"
	if len(labels) == 1 {
		title = labels[0] + " quota available again"
	}
	lines = append(lines, "Checked "+now.Local().Format("15:04"))
	return title, strings.Join(lines, "\n")
}
