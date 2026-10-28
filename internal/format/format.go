// Package format provides shared formatting utilities.
package format

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// FormatDuration formats a duration as a human-readable string like "2d5h", "3h04m", or "17m".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "now"
	}
	if d > 0 && d < time.Minute {
		return "<1m"
	}
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 30 {
		months := days / 30
		remainDays := days % 30
		if remainDays > 0 {
			return fmt.Sprintf("%dmo%dd", months, remainDays)
		}
		return fmt.Sprintf("%dmo", months)
	}
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// Percent rounds half up, retaining decimals near risk thresholds so a value
// below or above a threshold cannot be displayed as exactly that threshold.
func Percent(pct float64) string {
	for _, boundary := range []float64{90, 100} {
		if math.Abs(pct-boundary) <= 1 && pct != boundary {
			rounded := math.Round(pct*100) / 100
			if rounded == boundary {
				if pct < boundary {
					return fmt.Sprintf("<%.0f%%", boundary)
				}
				return fmt.Sprintf(">%.0f%%", boundary)
			}
			return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", rounded), "0"), ".") + "%"
		}
	}
	return fmt.Sprintf("%.0f%%", math.Round(pct))
}
