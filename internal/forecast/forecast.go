package forecast

import (
	"fmt"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/format"
)

const (
	FiveHourWindow = 5 * time.Hour
	SevenDayWindow = 7 * 24 * time.Hour

	// paceWidth is the fixed column width before an extra run-out note.
	// Must be >= the longest output of PaceLabel.
	paceWidth = 18
)

type Projection struct {
	// ProjectedPct is the estimated utilization at window reset (0-100+).
	ProjectedPct float64
	// OnTrack is true if projected usage stays under 100% at reset.
	OnTrack bool
	// WillLastToReset is true if current rate won't exhaust quota before reset.
	WillLastToReset bool
	// RunsOutIn is how long until quota is exhausted at current rate (zero if WillLastToReset).
	RunsOutIn time.Duration
	// RunsOutEarlyBy is how long before reset the quota is exhausted at current rate.
	RunsOutEarlyBy time.Duration
}

// Project estimates where utilization will be at window reset.
// currentPct is 0-100, resetsAt is when the window resets,
// windowLen is the total window duration (5h or 7d).
func Project(currentPct float64, resetsAt time.Time, windowLen time.Duration) Projection {
	remaining := time.Until(resetsAt)
	elapsed := windowLen - remaining

	if elapsed <= 0 || currentPct <= 0 {
		return Projection{
			ProjectedPct:    currentPct,
			OnTrack:         true,
			WillLastToReset: true,
		}
	}

	rate := currentPct / elapsed.Seconds()
	projected := rate * windowLen.Seconds()

	// ETA: how long until 100% at current rate
	willLast := true
	var runsOutIn time.Duration
	var runsOutEarlyBy time.Duration
	if currentPct >= 100 {
		willLast = false
		runsOutEarlyBy = remaining
	} else if rate > 0 {
		secsToExhaust := (100 - currentPct) / rate
		if secsToExhaust < remaining.Seconds() {
			willLast = false
			runsOutIn = time.Duration(secsToExhaust * float64(time.Second))
			runsOutEarlyBy = remaining - runsOutIn
		}
	}

	return Projection{
		ProjectedPct:    projected,
		OnTrack:         projected < 100,
		WillLastToReset: willLast,
		RunsOutIn:       runsOutIn,
		RunsOutEarlyBy:  runsOutEarlyBy,
	}
}

// Indicator returns a short status string for the projection.
func (p Projection) Indicator() string {
	return fmt.Sprintf("%.0f%%", p.ProjectedPct)
}

// PaceIndicator returns a human-readable usage estimate for the reset window.
func (p Projection) PaceIndicator() string {
	left := PaceLabel(p.ProjectedPct)
	if !p.WillLastToReset {
		if p.RunsOutIn > 0 {
			return fmt.Sprintf("%-*s · runs out in %s", paceWidth, left, format.FormatDuration(p.RunsOutIn))
		}
		if p.RunsOutEarlyBy > 0 {
			return fmt.Sprintf("%-*s · out now", paceWidth, left)
		}
	}
	return left
}

// PaceLabel summarizes the projected usage at reset. This is the same value
// Auto uses to choose the tray icon target.
func PaceLabel(projectedPct float64) string {
	return fmt.Sprintf("est. %.0f%% at reset", projectedPct)
}

// ColorIndicator returns an ANSI-colored indicator with pace info.
func (p Projection) ColorIndicator() string {
	pace := p.PaceIndicator()
	switch {
	case p.ProjectedPct >= 100:
		return fmt.Sprintf("\033[31m⚠ %s\033[0m", pace)
	case p.ProjectedPct >= 90:
		return fmt.Sprintf("\033[33m~ %s\033[0m", pace)
	default:
		return fmt.Sprintf("\033[32m✓ %s\033[0m", pace)
	}
}

// GuessWindowType infers the window duration from a window name string.
func GuessWindowType(name string) time.Duration {
	switch {
	case name == "5h":
		return FiveHourWindow
	case strings.HasPrefix(name, "7d"):
		return SevenDayWindow
	case strings.HasPrefix(name, "24h"):
		return 24 * time.Hour
	default:
		return 24 * time.Hour // default to daily
	}
}
