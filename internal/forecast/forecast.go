package forecast

import "time"

const (
	FiveHourWindow = 5 * time.Hour
	SevenDayWindow = 7 * 24 * time.Hour
)

type Projection struct {
	// ProjectedPct is the estimated utilization at window reset (0-100+).
	ProjectedPct float64
	// OnTrack is true if projected usage stays under 100% at reset.
	OnTrack bool
}

// Project estimates where utilization will be at window reset.
// currentPct is 0-100, resetsAt is when the window resets,
// windowLen is the total window duration (5h or 7d).
func Project(currentPct float64, resetsAt time.Time, windowLen time.Duration) Projection {
	remaining := time.Until(resetsAt)
	elapsed := windowLen - remaining

	if elapsed <= 0 || currentPct <= 0 {
		return Projection{ProjectedPct: currentPct, OnTrack: true}
	}

	rate := currentPct / elapsed.Seconds()
	projected := rate * windowLen.Seconds()

	return Projection{
		ProjectedPct: projected,
		OnTrack:      projected < 100,
	}
}

// Indicator returns a short status string for the projection.
func (p Projection) Indicator() string {
	switch {
	case p.ProjectedPct >= 100:
		return "over limit"
	case p.ProjectedPct >= 90:
		return "tight"
	default:
		return "on track"
	}
}

// ColorIndicator returns an ANSI-colored indicator.
func (p Projection) ColorIndicator() string {
	switch {
	case p.ProjectedPct >= 100:
		return "\033[31m⚠ over limit\033[0m"
	case p.ProjectedPct >= 90:
		return "\033[33m~ tight\033[0m"
	default:
		return "\033[32m✓ on track\033[0m"
	}
}
