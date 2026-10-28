package tray

// The tray's pure renderers: each reads one view snapshot and returns text or
// icon state. They touch no native menu and no file, so they build and test
// without the tray tag. Surfaces format a snapshot; they do not decide states.

import (
	"fmt"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
	"github.com/tnunamak/clawmeter/internal/view"
)

// selectedWindow returns the account and window the snapshot selects for the
// icon and tooltip, or false when no account has a quota window.
func selectedWindow(snap view.Snapshot) (*view.Account, view.Window, bool) {
	if snap.Selected == nil {
		return nil, view.Window{}, false
	}
	acct := accountNamed(snap, snap.Selected.AccountKey)
	if acct == nil {
		return nil, view.Window{}, false
	}
	for _, w := range acct.Windows {
		if w.Name == snap.Selected.Window {
			return acct, w, true
		}
	}
	return nil, view.Window{}, false
}

// accountNamed returns the snapshot account with the source key, or nil.
func accountNamed(snap view.Snapshot, key string) *view.Account {
	for i := range snap.Accounts {
		if snap.Accounts[i].Key == key {
			return &snap.Accounts[i]
		}
	}
	return nil
}

func IconState(snap view.Snapshot) (string, icons.MeterState) {
	acct, w, ok := selectedWindow(snap)
	if !ok {
		// No quota window to draw: the logo names the account the tray falls
		// back to, and an expired credential still draws its warning meter.
		if acct := fallbackAccount(snap); acct != nil {
			return acct.Provider, readingMeter(acct, view.Window{})
		}
		return "", icons.MeterState{}
	}
	return acct.Provider, readingMeter(acct, w)
}

// readingMeter is the meter of one window of an account, or of the account
// alone when w is zero. Retained readings draw the same last-known values as
// the menu; the snapshot has already aged their forecasts. Errors without a
// retained reading draw no meter. Expired credentials draw a warning meter.
func readingMeter(acct *view.Account, w view.Window) icons.MeterState {
	switch acct.Reading.State {
	case view.ReadingExpired:
		return icons.MeterState{UsagePct: 100, ExpectedPct: 100, RiskPct: 100}
	case view.ReadingStale:
		if acct.Reading.ObservedAt.IsZero() {
			return icons.MeterState{}
		}
		fallthrough
	case view.ReadingFresh:
		// A window whose reset has passed is not the current window: its usage
		// is the last period's, so the meter stays empty.
		if w.Name == "" || (w.Reset.Known && w.Reset.In <= 0) {
			return icons.MeterState{}
		}
		return icons.MeterState{
			UsagePct:     w.Used,
			ExpectedPct:  w.Pace,
			RiskPct:      riskPct(w),
			ShowExpected: w.HasPace,
			Label:        windowBadgeLabel(w.Label),
		}
	}
	return icons.MeterState{}
}

// fallbackAccount is the account the tray shows when no account has a quota
// window: an expired credential first, then an errored reading, then the first
// account with any reading. It is nil when no account has a reading.
func fallbackAccount(snap view.Snapshot) *view.Account {
	rank := func(a *view.Account) int {
		switch a.Reading.State {
		case view.ReadingExpired:
			return 0
		case view.ReadingError:
			return 1
		case view.ReadingUnavailable:
			return 3
		}
		return 2
	}
	var best *view.Account
	for i := range snap.Accounts {
		a := &snap.Accounts[i]
		if best == nil || rank(a) < rank(best) {
			best = a
		}
	}
	if best == nil || rank(best) == 3 {
		return nil
	}
	return best
}

// riskPct is the projected use the icon's risk channel draws. An exhausted
// window keeps its risk even while its forecast is still warming up.
func riskPct(w view.Window) float64 {
	risk := 0.0
	if w.Forecast.State == view.ForecastKnown {
		risk = w.Forecast.Pct
	}
	if w.Exhaustion.State == view.ExhaustionOut && w.Used > risk {
		risk = w.Used
	}
	return risk
}

// AtRisk reports whether a window's menu row carries the risk mark: it is used
// up, or the current rate runs it out before the reset.
func AtRisk(w view.Window) bool {
	return w.Exhaustion.State == view.ExhaustionOut || w.Exhaustion.State == view.ExhaustionRunsOut
}

func Tooltip(snap view.Snapshot, data map[string]*provider.UsageData) string {
	acct, w, ok := selectedWindow(snap)
	if !ok {
		if fb := fallbackAccount(snap); fb != nil {
			return appendResetCreditTooltip(accountHead(fb), data[fb.Key], snap.At)
		}
		return ""
	}
	raw := data[acct.Key]
	if head := accountHead(acct); acct.Reading.State == view.ReadingExpired || acct.Reading.State == view.ReadingError {
		return appendResetCreditTooltip(head, raw, snap.At)
	}

	lines := []string{iconTooltipTitle(acct.Label, w)}
	if acct.Reading.State == view.ReadingStale {
		lines = append(lines, staleLine(acct))
	}
	lines = append(lines,
		"Current "+format.Percent(w.Used),
		exhaustionLine(w, snap.At),
		resetLine(w),
	)
	if line := forecastLine(w); line != "" {
		lines = append(lines, line)
	}
	return appendResetCreditTooltip(strings.Join(lines, "\n"), raw, snap.At)
}

// AppendUpdateLine adds the pending-update line to a tooltip. An empty version
// adds nothing.
func AppendUpdateLine(tooltip, version string) string {
	if version == "" {
		return tooltip
	}
	line := fmt.Sprintf("Update available: %s", version)
	if strings.TrimSpace(tooltip) == "" {
		return line
	}
	return tooltip + "\n" + line
}

// accountHead is an account's one-line state when it has no quota to show:
// its name and why its reading cannot be used.
func accountHead(acct *view.Account) string {
	switch acct.Reading.State {
	case view.ReadingExpired:
		msg := "token expired"
		if acct.Reading.Reason != "" {
			msg = acct.Reading.Reason
		}
		return fmt.Sprintf("%s: %s", acct.Label, msg)
	case view.ReadingError:
		return fmt.Sprintf("%s: %s", acct.Label, acct.Reading.Reason)
	}
	return acct.Label
}

// staleLine says that the windows below are the last good reading, and when it was taken.
func staleLine(acct *view.Account) string {
	if !acct.Reading.ObservedAt.IsZero() {
		return fmt.Sprintf("stale - showing last good data from %s (%s)", acct.Reading.ObservedAt.Local().Format("Jan 2, 2006 3:04 PM"), acct.Reading.Reason)
	}
	return fmt.Sprintf("stale - showing last good data (%s)", acct.Reading.Reason)
}

// exhaustionLine says when the window runs out, or that it cannot say.
func exhaustionLine(w view.Window, at time.Time) string {
	switch w.Exhaustion.State {
	case view.ExhaustionOut, view.ExhaustionRunsOut:
		note := w.ExhaustionWords()
		if w.Exhaustion.State == view.ExhaustionOut {
			note = ranOutNote(note, w.Exhaustion.OutSince, at)
		}
		if w.Exhaustion.State == view.ExhaustionRunsOut {
			return "At average pace since reset: " + note
		}
		return upperFirst(note)
	case view.ExhaustionLasts:
		return "At average pace since reset: lasts to reset"
	}
	return "Run-out unknown"
}

// resetLine is the reset: a countdown, the provider's policy, or that it is not reported.
func resetLine(w view.Window) string {
	switch {
	case w.Reset.Known && w.Reset.In <= 0:
		return "Reset passed"
	case w.Reset.Known:
		return "Resets in " + format.FormatDuration(w.Reset.In)
	case w.Reset.Policy != "":
		return upperFirst(w.Reset.Policy)
	}
	return "Reset not reported"
}

// forecastLine is the forecast at reset, or why it is unavailable.
func forecastLine(w view.Window) string {
	words := w.ForecastWords()
	if w.Forecast.State == view.ForecastKnown {
		return upperFirst(words)
	}
	return words
}

func MenuRows(snap view.Snapshot, limit int) map[string][]string {
	byAccount := make(map[string][]meter.Row, len(snap.Accounts))
	var shown []meter.Row
	for i := range snap.Accounts {
		acct := &snap.Accounts[i]
		if !menuShowsRows(acct) {
			continue
		}
		rows := acct.Rows()
		if len(rows) > limit {
			rows = rows[:limit]
		}
		byAccount[acct.Key] = rows
		shown = append(shown, rows...)
	}
	widths := trayColumnWidths(shown)
	out := make(map[string][]string, len(byAccount))
	for key, rows := range byAccount {
		for _, row := range rows {
			out[key] = append(out[key], trayRowText(row, widths, snap.At))
		}
	}
	return out
}

// MenuMore counts, per account, the shown windows past the menu's row limit.
// The menu says "+N more" for them, so none is dropped without a trace. Idle
// windows are not counted: no list surface shows them.
func MenuMore(snap view.Snapshot, limit int) map[string]int {
	more := make(map[string]int)
	for i := range snap.Accounts {
		acct := &snap.Accounts[i]
		if !menuShowsRows(acct) {
			continue
		}
		shown := len(acct.ShownWindows())
		if n := shown - min(shown, limit); n > 0 {
			more[acct.Key] = n
		}
	}
	return more
}

// menuShowsRows reports whether a reading's quota rows show in the menu. An
// expired, errored or missing reading shows its problem instead.
func menuShowsRows(acct *view.Account) bool {
	switch acct.Reading.State {
	case view.ReadingUnavailable, view.ReadingExpired, view.ReadingError:
		return false
	}
	return true
}

// MoreText is the menu's text for windows it does not draw.
func MoreText(n int) string {
	return fmt.Sprintf("+%d more", n)
}

// figureSpace is as wide as a digit, so padded numbers line up in the
// menu's proportional font.
const figureSpace = " "

// trayGutter separates a row's columns. Columns line up, so they need space
// between them, not punctuation.
const trayGutter = "    "

// trayWidths are the widest values in the menu, in runes, so each column
// starts at the same place on every row: an early forecast such as "3360%"
// widens its column instead of pushing its own row out of line. Times are
// not padded: their letters are not as wide as digits, and they come last.
type trayWidths struct {
	used, forecast int
}

func trayColumnWidths(rows []meter.Row) trayWidths {
	widths := trayWidths{used: len("100%"), forecast: len("100%")}
	for _, row := range rows {
		widths.forecast = max(widths.forecast, len([]rune(row.Forecast)), len([]rune(row.ForecastState))-len(" at reset"))
		widths.used = max(widths.used, len([]rune(row.Used)))
	}
	return widths
}

// trayRowText writes a row for a native menu, which has no color and no
// columns: labeled values padded with figure spaces and separated by
// gutters. now is the snapshot's clock, for the run-out date. Research: divan
// ai-quota-monitoring/compact-quota-row-typography-for-menus-and-terminal-
// tables.md.
func trayRowText(row meter.Row, widths trayWidths, now time.Time) string {
	if row.Line != "" {
		return row.Line
	}
	bar := meter.BarText(row.Bar)
	if !row.HasPace() {
		bar += figureSpace // as wide as the pace mark, so the columns still line up
	}
	forecastText := "no forecast"
	switch {
	case row.Forecast != "":
		forecastText = meter.Pad(row.Forecast, widths.forecast, figureSpace) + " at reset"
	case row.ForecastState != "":
		forecastText = meter.Pad(row.ForecastState, widths.forecast+len(" at reset"), figureSpace)
	}
	resets := row.Resets
	if !row.ResetPolicy {
		resets = "resets in " + row.Resets
	}
	columns := []string{row.Code, bar, meter.Pad(row.Used, widths.used, figureSpace) + " used", forecastText, resets}
	switch row.RunsOut {
	case "":
	case "now":
		columns = append(columns, ranOutLabel(row.RanOutAt, now))
	default:
		columns = append(columns, "runs out in "+row.RunsOut)
	}
	for _, detail := range []string{row.Qualifier, row.Money} {
		if detail != "" {
			columns = append(columns, detail)
		}
	}
	text := strings.Join(columns, trayGutter)
	if row.Forecast != "" {
		text += "; forecasts: " + forecast.AverageBasis
	}
	return text
}

// runOutSince writes when a window ran out: a time of day when it was today,
// otherwise a date and time.
func runOutSince(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	clock := at.Format("15:04")
	if at.Year() == now.Year() && at.YearDay() == now.YearDay() {
		return clock
	}
	return at.Format("Jan 2") + " " + clock
}

// ranOutLabel is the menu's run-out column for a window that is already out.
func ranOutLabel(ranOutAt, now time.Time) string {
	if ranOutAt.IsZero() {
		return "out now"
	}
	return "out since " + runOutSince(ranOutAt, now)
}

// ranOutNote replaces the forecast's leading "out now" with the time the
// tray saw the window run out. Any wait after it is kept.
func ranOutNote(note string, ranOutAt, now time.Time) string {
	if ranOutAt.IsZero() || !strings.HasPrefix(note, "out now") {
		return note
	}
	return "out since " + runOutSince(ranOutAt, now) + strings.TrimPrefix(note, "out now")
}

func appendResetCreditTooltip(tooltip string, data *provider.UsageData, now time.Time) string {
	if resetLine := resetCreditTraySummary(data, now); resetLine != "" {
		tooltip += "\n" + resetLine
	}
	if announced := announcementSummary(data, now); announced != "" {
		tooltip += "\n" + announced
	}
	return tooltip
}

// announcementSummary is the tray form of an announced reset, empty when none
// bears on the account.
func announcementSummary(data *provider.UsageData, now time.Time) string {
	if data == nil {
		return ""
	}
	return data.AnnouncedReset.Summary(now)
}

func resetCreditTraySummary(data *provider.UsageData, now time.Time) string {
	if data == nil || data.ResetCredits == nil || (data.Stale && !data.ResetCredits.Snapshot) {
		return ""
	}
	return data.ResetCredits.Summary(now)
}

func upperFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func iconTooltipTitle(display string, w view.Window) string {
	windowLabel := humanWindowLabelFor(w.Name, w.Label)
	if windowLabel == "" {
		return display
	}
	if display == "" {
		return windowLabel
	}
	return display + " " + windowLabel
}

// humanWindowLabel is a provider window's label in the tooltip, such as "5-Hour".
func humanWindowLabel(window provider.UsageWindow) string {
	return humanWindowLabelFor(window.Name, meter.Label(window))
}

// humanWindowLabelFor names a window in a tooltip, from its internal name and its label.
func humanWindowLabelFor(name, label string) string {
	switch strings.TrimSpace(name) {
	case "5h":
		return "5-Hour"
	case "7d":
		return "7-Day"
	case "7d All":
		return "7-Day All Models"
	case "7d OAuth":
		return "7-Day OAuth Apps"
	case "7d Opus":
		return "7-Day Opus"
	case "7d Sonnet":
		return "7-Day Sonnet"
	}
	switch strings.ToLower(label) {
	case "5 hours":
		return "5-Hour"
	case "7 days":
		return "7-Day"
	case "7 days (all models)":
		return "7-Day All Models"
	case "7 days (oauth apps)":
		return "7-Day OAuth Apps"
	case "7 days (opus)":
		return "7-Day Opus"
	case "7 days (sonnet)":
		return "7-Day Sonnet"
	}
	return label
}

// windowBadgeLabel is the two-character badge a tray icon shows for a window.
func windowBadgeLabel(name string) string {
	label := strings.ToUpper(strings.TrimSpace(name))
	// A two-character badge cannot safely abbreviate a multi-digit duration.
	if len(label) > 1 && label[0] >= '0' && label[0] <= '9' && label[1] >= '0' && label[1] <= '9' {
		return "--"
	}
	parts := strings.FieldsFunc(label, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
	if len(parts) > 0 && len(parts[0]) >= 2 && parts[0][0] >= '0' && parts[0][0] <= '9' {
		if len(parts) > 1 && len(parts[1]) > 0 {
			return string([]byte{parts[0][0], parts[1][0]})
		}
		return parts[0][:2]
	}
	var b strings.Builder
	for _, r := range label {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			if b.Len() >= 2 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "--"
	}
	return b.String()
}
