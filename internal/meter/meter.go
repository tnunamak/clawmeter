// Package meter lays out quota windows for display. The tray menu and the CLI
// status table both draw its rows, so a quota reads the same in each: window
// code, a ten-cell bar with a pace mark, where the forecast ends relative to
// the limit, use now, and the times.
//
// Research: divan ai-quota-monitoring/fixed-quota-scale-text-bars-need-
// explicit-numeric-forecast-overflow.md and glanceable-quota-severity-in-
// native-tray-menus.md. If the ▒ shading confuses people, the fallback is a
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
	active := make([]provider.UsageWindow, 0, len(windows))
	for _, window := range windows {
		proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.WindowLength(window))
		idle := window.Utilization == 0 && (proj.Unknown || proj.ProjectedPct < 0.5)
		if !idle {
			active = append(active, window)
		}
	}
	if len(active) == 0 {
		return windows
	}
	return active
}

// FieldKind says what a row field holds, so a renderer can color it.
type FieldKind int

const (
	// Margin is "+87% at reset" or "−3% at reset".
	Margin FieldKind = iota
	// Note stands in for the margin when there is no forecast.
	Note
	// Now is current use.
	Now
	// Resets is when the window resets, or its reset policy.
	Resets
	// RunsOut is when the window runs out before its reset.
	RunsOut
	// Detail is a qualifier or money detail.
	Detail
)

// Field is one column of a row.
type Field struct {
	Text string
	Kind FieldKind
}

// Row is one quota window laid out for display. A row with Line set is a
// money-only window and has no bar or fields.
type Row struct {
	Code   string
	Bar    []Cell
	Fields []Field
	Line   string
	// Over is true when the forecast passes the limit.
	Over bool
}

// Layout pads row fields so columns line up. Space is the padding
// character: a figure space, as wide as a digit, for proportional fonts.
type Layout struct {
	Space       string
	MarginWidth int
}

// DefaultMarginWidth fits margins up to "+999%".
const DefaultMarginWidth = 5

// Rows lays out an account's windows, each with its projection. A window's
// qualifier, such as "all models", shows only when it is needed: "all
// models" on an account's only seven-day row says nothing.
func Rows(windows []provider.UsageWindow, projections []forecast.Projection, layout Layout) []Row {
	codes := make(map[string]int, len(windows))
	for _, window := range windows {
		codes[Code(window)]++
	}
	rows := make([]Row, len(windows))
	for i, window := range windows {
		rows[i] = NewRow(window, projections[i], codes[Code(window)] > 1, layout)
	}
	return rows
}

// MarginWidth is the widest margin among the projections, at least
// DefaultMarginWidth, so an early forecast such as "+3260%" widens the
// column for every row instead of pushing its own row out of line. Use now
// and the times keep fixed widths: they pass them only past 999% used or 99
// days.
func MarginWidth(projections []forecast.Projection) int {
	width := DefaultMarginWidth
	for _, proj := range projections {
		// A window with no reset has a zero projection; its "−100%" fits the
		// default width.
		if !proj.Unknown && !proj.Stale {
			width = max(width, len([]rune(MarginText(proj.ProjectedPct))))
		}
	}
	return width
}

// NewRow lays out one window. proj must come from the window; it is ignored
// when the window has no reset.
func NewRow(window provider.UsageWindow, proj forecast.Projection, codeShared bool, layout Layout) Row {
	label := Label(window)
	money := window.MoneyDetail()
	if money != "" && window.ResetsAt.IsZero() && window.ResetPolicy == "" {
		return Row{Line: label + ": " + money}
	}
	var tail []Field
	if qualifier := Qualifier(window); qualifier != "" && (codeShared || qualifier != "all models") {
		tail = append(tail, Field{qualifier, Detail})
	}
	if money != "" {
		tail = append(tail, Field{money, Detail})
	}
	length := forecast.WindowLength(window)
	hasPace := length > 0 && !window.ResetsAt.IsZero()
	pace := 0.0
	if hasPace {
		pace = ExpectedPct(window.ResetsAt, length)
	}
	now := Field{Pad(fmt.Sprintf("%.0f", window.Utilization), 3, layout.Space) + "% now", Now}
	row := Row{Code: Code(window)}
	if window.ResetsAt.IsZero() {
		reset := window.ResetPolicy
		if reset == "" {
			reset = "reset not reported"
		}
		row.Bar = Bar(window.Utilization, window.Utilization, pace, hasPace)
		row.Fields = append([]Field{now, {reset, Resets}}, tail...)
		return row
	}
	resets := Field{"resets in " + Duration(time.Until(window.ResetsAt), layout.Space), Resets}
	if proj.Unknown || proj.Stale {
		row.Bar = Bar(window.Utilization, window.Utilization, pace, hasPace)
		row.Fields = append([]Field{{proj.PaceIndicator(), Note}, now, resets}, tail...)
		return row
	}
	row.Bar = Bar(window.Utilization, proj.ProjectedPct, pace, hasPace)
	row.Over = proj.ProjectedPct > 100
	row.Fields = []Field{{Pad(MarginText(proj.ProjectedPct), layout.MarginWidth, layout.Space) + " at reset", Margin}, now, resets}
	if !proj.WillLastToReset {
		if proj.RunsOutIn > 0 {
			row.Fields = append(row.Fields, Field{"runs out in " + Duration(proj.RunsOutIn, layout.Space), RunsOut})
		} else {
			row.Fields = append(row.Fields, Field{"out now", RunsOut})
		}
	}
	row.Fields = append(row.Fields, tail...)
	return row
}

// MarginText says where the forecast ends relative to the limit: "+87%"
// over it or "−3%" under it. It decides on the raw forecast, so 100.4% is
// "+<1%", not a rounded "+0%".
func MarginText(projectedPct float64) string {
	margin := projectedPct - 100
	sign := "+"
	if margin <= 0 {
		sign, margin = "−", -margin
	}
	if margin < 0.5 {
		return sign + "<1%"
	}
	return sign + fmt.Sprintf("%.0f", margin) + "%"
}

// Duration writes a duration as two units, " 3d18h" or "12h04m", padded so
// the times line up from row to row.
func Duration(d time.Duration, space string) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Minute)
	hours := int(d / time.Hour)
	if hours >= 24 {
		return Pad(strconv.Itoa(hours/24), 2, space) + "d" + Pad(strconv.Itoa(hours%24), 2, space) + "h"
	}
	return Pad(strconv.Itoa(hours), 2, space) + fmt.Sprintf("h%02dm", int(d/time.Minute)%60)
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

// Code names a window by its length, such as "5H" or "7D", so bars start
// at the same place. A window with no known length keeps its label.
func Code(window provider.UsageWindow) string {
	length := forecast.WindowLength(window)
	switch {
	case length >= 24*time.Hour && length%(24*time.Hour) == 0:
		return fmt.Sprintf("%dD", length/(24*time.Hour))
	case length >= time.Hour && length%time.Hour == 0:
		return fmt.Sprintf("%dH", length/time.Hour)
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
