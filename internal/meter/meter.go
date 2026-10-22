// Package meter lays out quota windows for display. The tray menu and the CLI
// status table both draw its rows, so a quota reads the same in each: window
// code, a ten-cell bar with a pace mark, the forecast use at reset, use now,
// and the times.
//
// Research (divan ai-quota-monitoring/): fixed-quota-scale-text-bars-need-
// explicit-numeric-forecast-overflow.md, glanceable-quota-severity-in-
// native-tray-menus.md and compact-quota-row-typography-for-menus-and-
// terminal-tables.md. If the ▒ shading confuses people, the fallback is a
// used-only bar with the forecast in words.
package meter

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// BarCells is the number of cells in a bar, each 10% of the limit.
const BarCells = 10

// CellKind is what a bar cell shows.
type CellKind int

const (
	// Used is usage up to an even pace (the tray icon's gray).
	Used CellKind = iota
	// Ahead is usage past an even pace (the tray icon's red).
	Ahead
	// Projected is further use forecast by the reset.
	Projected
	// Free is the rest of the limit.
	Free
	// Pace is the mark where an even pace would have used by now.
	Pace
)

// Cell is one glyph of a bar. Behind marks the cells between the end of
// usage and the pace mark: the tray icon's green.
type Cell struct {
	Glyph  string
	Kind   CellKind
	Behind bool
}

// Bar draws BarCells cells on a fixed 0–100% scale: █ used, ▒ projected by
// reset, ░ free. When hasPace is true, ┃ is inserted where an even pace
// would have used by now, like the comparative mark on a bullet graph.
// Values past 100% fill the bar; the margin text says by how much.
func Bar(usedPct, projectedPct, pacePct float64, hasPace bool) []Cell {
	used := cells(usedPct)
	projected := max(used, cells(projectedPct))
	pace := cells(pacePct)
	bar := make([]Cell, 0, BarCells+1)
	for i := range BarCells {
		if hasPace && i == pace {
			bar = append(bar, Cell{Glyph: "┃", Kind: Pace})
		}
		cell := Cell{Glyph: "░", Kind: Free}
		switch {
		case i < used && hasPace && i >= pace:
			cell = Cell{Glyph: "█", Kind: Ahead}
		case i < used:
			cell = Cell{Glyph: "█", Kind: Used}
		case i < projected:
			cell = Cell{Glyph: "▒", Kind: Projected}
		}
		cell.Behind = hasPace && i >= used && i < pace
		bar = append(bar, cell)
	}
	if hasPace && pace == BarCells {
		bar = append(bar, Cell{Glyph: "┃", Kind: Pace})
	}
	return bar
}

// BarText is a bar's glyphs without color.
func BarText(bar []Cell) string {
	var b strings.Builder
	for _, cell := range bar {
		b.WriteString(cell.Glyph)
	}
	return b.String()
}

func cells(pct float64) int {
	if math.IsNaN(pct) || math.IsInf(pct, 0) {
		return 0
	}
	return int(math.Round(math.Max(0, math.Min(100, pct)) / (100 / BarCells)))
}

// ExpectedPct is how much an even pace would have used by now.
func ExpectedPct(resetsAt time.Time, length time.Duration) float64 {
	if length <= 0 {
		return 0
	}
	elapsed := length - time.Until(resetsAt)
	if elapsed <= 0 {
		return 0
	}
	if elapsed >= length {
		return 100
	}
	return elapsed.Seconds() / length.Seconds() * 100
}

// Shown drops windows with nothing used and nothing projected, unless that
// would leave no window to show.
func Shown(windows []provider.UsageWindow) []provider.UsageWindow {
	return ShownAt(windows, time.Now())
}

// ShownAt is Shown judged at the given time.
func ShownAt(windows []provider.UsageWindow, at time.Time) []provider.UsageWindow {
	active := make([]provider.UsageWindow, 0, len(windows))
	for _, window := range windows {
		if !IdleAt(window, at) {
			active = append(active, window)
		}
	}
	if len(active) == 0 {
		return windows
	}
	return active
}

// IdleAt reports whether a window has nothing used and nothing projected at
// the given time. Shown leaves idle windows out of a list.
func IdleAt(window provider.UsageWindow, at time.Time) bool {
	proj := forecast.ProjectAt(window.Utilization, window.ResetsAt, forecast.WindowLength(window), at)
	return window.Utilization == 0 && (proj.Unknown || proj.ProjectedPct < 0.5)
}

// Row is one quota window ready to display. Values are unpadded; each
// renderer lays them out: the tray menu as labeled phrases, the CLI as a
// table. A row with Line set is a money-only window and has nothing else.
type Row struct {
	Line string

	Code string
	// Qualifier is what the code leaves out of the label, such as "Fable",
	// when it is needed to tell windows apart.
	Qualifier string
	// Money is a quota window's money detail.
	Money string
	Bar   []Cell
	// Forecast is the forecast use at reset, such as "137%". When the
	// window has a reset but no usable forecast, it is empty and
	// ForecastState says why ("unknown" or "stale").
	Forecast      string
	ForecastState string
	// Over is true when the forecast passes the limit.
	Over bool
	Used string
	// Resets is the time to the reset, such as "2h 23m", or the reset
	// policy when ResetPolicy is true.
	Resets      string
	ResetPolicy bool
	// RunsOut is the time until the window runs out before its reset,
	// "now" when it already has, or empty when it lasts to the reset.
	RunsOut string
	// RanOutAt is when the window ran out, if a renderer knows it. Only a
	// row whose RunsOut is "now" can have it. Zero means the time is unknown.
	RanOutAt time.Time
}

// HasPace reports whether the bar carries a pace mark.
func (r Row) HasPace() bool {
	return len(r.Bar) > BarCells
}

// Rows lays out an account's windows, each with its projection. A window's
// qualifier, such as "all models", shows only when it is needed: "all
// models" on an account's only seven-day row says nothing.
func Rows(windows []provider.UsageWindow, projections []forecast.Projection) []Row {
	codes := make(map[string]int, len(windows))
	for _, window := range windows {
		codes[Code(window)]++
	}
	rows := make([]Row, len(windows))
	for i, window := range windows {
		rows[i] = NewRow(window, projections[i], codes[Code(window)] > 1)
	}
	return rows
}

// NewRow lays out one window. proj must come from the window; it is ignored
// when the window has no reset.
func NewRow(window provider.UsageWindow, proj forecast.Projection, codeShared bool) Row {
	label := Label(window)
	money := window.MoneyDetail()
	if money != "" && window.ResetsAt.IsZero() && window.ResetPolicy == "" {
		return Row{Line: label + ": " + money}
	}
	row := Row{Code: Code(window), Money: money, Used: Percent(window.Utilization)}
	if qualifier := Qualifier(window); qualifier != "" && (codeShared || qualifier != "all models") {
		row.Qualifier = qualifier
	}
	length := forecast.WindowLength(window)
	hasPace := length > 0 && !window.ResetsAt.IsZero()
	pace := 0.0
	if hasPace {
		pace = ExpectedPct(window.ResetsAt, length)
	}
	if window.ResetsAt.IsZero() {
		row.Resets, row.ResetPolicy = window.ResetPolicy, true
		if row.Resets == "" {
			row.Resets = "reset not reported"
		}
		row.Bar = Bar(window.Utilization, window.Utilization, pace, hasPace)
		return row
	}
	row.Resets = Duration(time.Until(window.ResetsAt))
	if proj.Unknown || proj.Stale {
		row.ForecastState = "unknown"
		if proj.Stale {
			row.ForecastState = "stale"
		} else if window.Utilization >= 100 {
			// A fresh window can be used up before its forecast warms up.
			row.RunsOut = "now"
		}
		row.Bar = Bar(window.Utilization, window.Utilization, pace, hasPace)
		return row
	}
	row.Bar = Bar(window.Utilization, proj.ProjectedPct, pace, hasPace)
	row.Forecast = Percent(proj.ProjectedPct)
	row.Over = proj.ProjectedPct > 100
	if !proj.WillLastToReset {
		row.RunsOut = "now"
		if proj.RunsOutIn > 0 {
			row.RunsOut = Duration(proj.RunsOutIn)
		}
	}
	return row
}

// Percent writes a whole percentage.
func Percent(pct float64) string {
	return strconv.FormatFloat(math.Round(pct), 'f', 0, 64) + "%"
}

// Duration writes a duration in at most two units with a space between
// them: "6d 16h", "2h 23m", "1h 1m", "59m". A zero second unit is left off
// ("7d", "2h"), and a duration under a minute is "<1m".
func Duration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	d = d.Round(time.Minute)
	minutes := int(d / time.Minute)
	days, hours, mins := minutes/(24*60), minutes/60%24, minutes%60
	two := func(n int, unit string, m int, sub string) string {
		if m == 0 {
			return strconv.Itoa(n) + unit
		}
		return strconv.Itoa(n) + unit + " " + strconv.Itoa(m) + sub
	}
	switch {
	case days > 0:
		return two(days, "d", hours, "h")
	case hours > 0:
		return two(hours, "h", mins, "m")
	}
	return strconv.Itoa(mins) + "m"
}

// Pad left-pads s to width runes with space.
func Pad(s string, width int, space string) string {
	return strings.Repeat(space, max(0, width-len([]rune(s)))) + s
}

// Label is the window's display name, falling back to its internal name.
func Label(window provider.UsageWindow) string {
	if label := strings.TrimSpace(window.DisplayName); label != "" {
		return label
	}
	return strings.TrimSpace(window.Name)
}

// Code names a window by its length, such as "5h" or "7d" as on the tray
// icon, so bars start at the same place. A window with no known length
// keeps its label.
func Code(window provider.UsageWindow) string {
	length := forecast.WindowLength(window)
	switch {
	case length >= 24*time.Hour && length%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", length/(24*time.Hour))
	case length >= time.Hour && length%time.Hour == 0:
		return fmt.Sprintf("%dh", length/time.Hour)
	}
	return Label(window)
}

// Qualifier is what the code leaves out of the label: the "all models" in
// "7 days (all models)", or a name such as "API key".
func Qualifier(window provider.UsageWindow) string {
	label := Label(window)
	if Code(window) == label {
		return ""
	}
	if open := strings.Index(label, " ("); open >= 0 && strings.HasSuffix(label, ")") {
		return label[open+2 : len(label)-1]
	}
	if genericLabel.MatchString(strings.ToLower(label)) {
		return ""
	}
	return label
}

// genericLabel matches labels that only state a length, such as "7 days",
// "5h" or "five_hour".
var genericLabel = regexp.MustCompile(`^(\d+|one|five|seven|thirty)?[\s_-]*(h|hours?|d|days?|hourly|daily|weekly|monthly)$`)
