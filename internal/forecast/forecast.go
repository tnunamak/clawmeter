package forecast

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/format"
)

const (
	FiveHourWindow = 5 * time.Hour
	SevenDayWindow = 7 * 24 * time.Hour

	// paceWidth is the fixed column width for the pace label (e.g. "100% behind").
	// Must be >= the longest output of the pace switch in PaceIndicator.
	paceWidth = 11
)

type Projection struct {
	// ProjectedPct is the estimated utilization at window reset (0-100+).
	ProjectedPct float64
	// OnTrack is true if projected usage stays under 100% at reset.
	OnTrack bool
	// Delta is the difference between actual and expected usage (positive = behind pace).
	Delta float64
	// WillLastToReset is true if current rate won't exhaust quota before reset.
	WillLastToReset bool
	// RunsOutIn is how long until quota is exhausted at current rate (zero if WillLastToReset).
	RunsOutIn time.Duration
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
	expected := (elapsed.Seconds() / windowLen.Seconds()) * 100
	delta := currentPct - expected

	// ETA: how long until 100% at current rate
	willLast := true
	var runsOutIn time.Duration
	if rate > 0 {
		secsToExhaust := (100 - currentPct) / rate
		if secsToExhaust < remaining.Seconds() {
			willLast = false
			runsOutIn = time.Duration(secsToExhaust * float64(time.Second))
		}
	}

	return Projection{
		ProjectedPct:    projected,
		OnTrack:         projected < 100,
		Delta:           delta,
		WillLastToReset: willLast,
		RunsOutIn:       runsOutIn,
	}
}

// Indicator returns a short status string for the projection.
func (p Projection) Indicator() string {
	return fmt.Sprintf("%.0f%%", p.ProjectedPct)
}

// PaceIndicator returns a human-readable pace summary like CodexBar.
func (p Projection) PaceIndicator() string {
	absDelta := math.Abs(p.Delta)

	var left string
	switch {
	case absDelta <= 2:
		left = "on pace"
	case p.Delta > 0:
		left = fmt.Sprintf("%.0f%% behind", absDelta)
	default:
		left = fmt.Sprintf("%.0f%% ahead", absDelta)
	}
	left = fmt.Sprintf("%-*s", paceWidth, left)

	var right string
	if p.WillLastToReset {
		right = "lasts to reset"
	} else if p.RunsOutIn > 0 && absDelta > 2 {
		right = "runs out " + shortDuration(p.RunsOutIn)
	}

	if right != "" {
		return left + " · " + right
	}
	return left
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
	default:
		return 24 * time.Hour // default to daily
	}
}

func shortDuration(d time.Duration) string {
	return "in " + format.FormatDuration(d)
}
