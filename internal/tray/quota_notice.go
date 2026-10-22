//go:build tray

package tray

import (
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
)

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
			if w.ResetsAt.IsZero() {
				continue
			}
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
