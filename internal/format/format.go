// Package format provides shared formatting utilities.
package format

import (
	"fmt"
	"time"
)

// FormatDuration formats a duration as a human-readable string like "2d5h", "3h04m", or "17m".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "now"
	}
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
