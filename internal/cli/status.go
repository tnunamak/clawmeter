package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	"github.com/tnunamak/clawmeter/internal/resetfeed"
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/view"
)

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

const (
	reset = "\033[0m"
	red   = "\033[31m"
	green = "\033[32m"
	bold  = "\033[1m"
	dim   = "\033[2m"
)

// ProviderFormatter handles formatting for a single provider's output.
type ProviderFormatter struct {
	Name string
	// Display is the name the human surfaces show: the provider's display
	// name, plus the source label when the family has several sources.
	Display string
	// DisplayName is the provider's display name alone. The shared view
	// adds the source label itself.
	DisplayName string
	// SourceCount is how many sources of this formatter's family were
	// configured when Display was made.
	SourceCount       int
	Family            string
	SourceID          string
	SourceLabel       string
	Data              *provider.UsageData
	Status            *status.ProviderStatus
	ExplicitlyEnabled bool
}

// tableLayout holds the column widths shared by every provider in the
// status table.
type tableLayout struct {
	provider, window, forecast, used, resets, runsOut int
}

// cellWidth is how many terminal cells s takes: CJK characters take two,
// combining marks none. East Asian ambiguous characters such as the bar's
// block glyphs count as one, as most terminals draw them.
var cellWidth = (&runewidth.Condition{EastAsianWidth: false}).StringWidth

// tableGutter separates the status table's columns.
const tableGutter = "  "

// tableHeader names the status table's columns. Numeric columns are
// right-aligned, as their values are. Research: divan ai-quota-monitoring/
// compact-quota-row-typography-for-menus-and-terminal-tables.md.
var tableHeader = tableCells{"PROVIDER", "WINDOW", "USAGE", "USED", "AT RESET", "RESETS IN", "RUNS OUT IN"}

type tableCells [7]string

func newTableLayout() tableLayout {
	h := tableHeader
	return tableLayout{
		provider: cellWidth(h[0]), window: cellWidth(h[1]),
		used: cellWidth(h[3]), forecast: cellWidth(h[4]),
		resets: cellWidth(h[5]), runsOut: cellWidth(h[6]),
	}
}

func (l *tableLayout) fit(cells tableCells) {
	l.provider = max(l.provider, cellWidth(cells[0]))
	l.window = max(l.window, cellWidth(cells[1]))
	l.used = max(l.used, cellWidth(cells[3]))
	l.forecast = max(l.forecast, cellWidth(cells[4]))
	l.resets = max(l.resets, cellWidth(cells[5]))
	l.runsOut = max(l.runsOut, cellWidth(cells[6]))
}

// line pads cells to the layout, right-aligning the numeric columns, and
// colors them with style (cell index to ANSI style). The bar arrives
// already colored, so its width is passed separately.
func (l tableLayout) line(cells tableCells, barWidth int, style map[int]string) string {
	left := func(s string, w int) string { return s + strings.Repeat(" ", max(0, w-cellWidth(s))) }
	right := func(s string, w int) string { return strings.Repeat(" ", max(0, w-cellWidth(s))) + s }
	padded := []string{
		left(cells[0], l.provider), left(cells[1], l.window),
		cells[2] + strings.Repeat(" ", max(0, meter.BarCells+1-barWidth)),
		right(cells[3], l.used), right(cells[4], l.forecast), right(cells[5], l.resets), right(cells[6], l.runsOut),
	}
	for i, st := range style {
		if st != "" && strings.TrimSpace(padded[i]) != "" {
			padded[i] = st + padded[i] + reset
		}
	}
	return strings.TrimRight(strings.Join(padded, tableGutter), " ")
}

// span starts free text, such as a credit balance or an error, at the
// WINDOW column.
func (l tableLayout) span(providerCell, text string) string {
	return providerCell + strings.Repeat(" ", max(0, l.provider-cellWidth(providerCell))) + tableGutter + text
}

// shownRows are the rows the status table shows for one source, read from its
// snapshot account. A reading that is expired, errored or missing has none.
func shownRows(acct *view.Account) []meter.Row {
	if acct == nil || acct.Reading.State == view.ReadingUnavailable || acct.Reading.State == view.ReadingExpired || acct.Reading.State == view.ReadingError {
		return nil
	}
	return acct.Rows()
}

// rowCells are a quota row's table cells, the bar left uncolored.
func rowCells(row meter.Row) tableCells {
	window := row.Code
	if row.Qualifier != "" {
		window += " " + row.Qualifier
	}
	forecast := row.Forecast
	switch {
	case forecast == "" && row.ForecastState != "":
		forecast = row.ForecastState
	case forecast == "":
		forecast = "—"
	}
	resets := row.Resets
	if row.ResetPolicy {
		resets = strings.TrimPrefix(resets, "resets ")
	}
	return tableCells{"", window, meter.BarText(row.Bar), row.Used, forecast, resets, row.RunsOut}
}

// FormatColorAligned returns the provider's rows of the status table, read from
// a snapshot of this provider alone. PrintColor reads one snapshot for all.
func (pf *ProviderFormatter) FormatColorAligned(layout tableLayout) []string {
	m := &MultiProviderOutput{Providers: []ProviderFormatter{*pf}}
	return m.buildView(time.Now()).colorLines(0, layout)
}

// colorLines returns the status table rows of the i-th source. Bars take the
// tray icon's colors: usage past an even pace is red, the gap short of it green.
func (v outputView) colorLines(i int, layout tableLayout) []string {
	pf := v.entries[i].pf
	acct := v.account(v.entries[i].key)
	now := v.snap.At
	var note string
	if pf.Status != nil && pf.Status.Indicator.HasIssue() {
		note = pf.Status.FormatCLI()
	}
	if pf.Data != nil && pf.Data.Stale {
		note = staleSummary(pf.Data)
	}
	var lines []string
	providerCell := func() string {
		if len(lines) == 0 {
			return v.display(v.entries[i].key)
		}
		return ""
	}
	addSpan := func(text string) { lines = append(lines, layout.span(providerCell(), text)) }

	switch {
	case pf.Data == nil:
		addSpan(dim + "no data" + reset)
	case acct.Reading.State == view.ReadingExpired || acct.Reading.State == view.ReadingError:
		problem := format.HumanizeError(pf.Data.Error)
		if acct.Reading.State == view.ReadingExpired {
			problem = expiredMessage(pf.Data.Error)
		}
		addSpan(red + problem + reset)
		for _, grant := range acct.CreditGrants {
			addSpan(grant.Summary())
		}
		lines = appendSnapshotLines(lines, pf.Data, layout, now)
	default:
		rows := shownRows(acct)
		for _, row := range rows {
			if row.Line != "" {
				addSpan(row.Line)
				continue
			}
			cells := rowCells(row)
			cells[0] = providerCell()
			cells[2] = colorBar(row.Bar)
			style := map[int]string{}
			if row.Over {
				style[4] = red
			}
			if row.Forecast == "" {
				style[4] = dim
			}
			style[6] = red
			line := layout.line(cells, len(row.Bar), style)
			if row.Money != "" {
				line += tableGutter + dim + row.Money + reset
			}
			lines = append(lines, line)
		}
		for _, grant := range acct.CreditGrants {
			addSpan(grant.Summary())
		}
		for _, balance := range pf.Data.Balances {
			name := balance.DisplayName
			if name == "" {
				name = balance.Name
			}
			addSpan(fmt.Sprintf("%s %.2f remaining", name, balance.Remaining))
		}
		for _, pool := range pf.Data.CreditPools {
			addSpan(pool.Name + ": " + pool.Summary(now))
		}
		if summary := resetCreditSummary(pf.Data, now); summary != "" {
			addSpan(summary)
		}
		if announced := announcementSummary(pf.Data, now); announced != "" {
			addSpan(announced)
		}
	}
	if note != "" {
		addSpan(dim + note + reset)
	}
	return lines
}

// colorBar draws a bar in the tray icon's colors.
func colorBar(bar []meter.Cell) string {
	var b strings.Builder
	for _, cell := range bar {
		style := ""
		switch {
		case cell.Kind == meter.Ahead:
			style = red
		case cell.Behind:
			style = green
		case cell.Kind == meter.Pace:
			style = bold
		case cell.Kind == meter.Projected || cell.Kind == meter.Free:
			style = dim
		}
		if style == "" {
			b.WriteString(cell.Glyph)
		} else {
			b.WriteString(style + cell.Glyph + reset)
		}
	}
	return b.String()
}

// FormatPlain returns plain text output for a provider, read from a snapshot of
// this provider alone. PrintPlain reads one snapshot for all of them.
func (pf *ProviderFormatter) FormatPlain() string {
	m := &MultiProviderOutput{Providers: []ProviderFormatter{*pf}}
	return m.buildView(time.Now()).plainLine(0)
}

// plainNoteWidth is the column before a run-out note. It must hold the longest
// forecast wording, "est. 100% at reset".
const plainNoteWidth = 18

// plainLine is the one-line status of the i-th source.
func (v outputView) plainLine(i int) string {
	pf := v.entries[i].pf
	acct := v.account(v.entries[i].key)
	display := v.display(v.entries[i].key)
	now := v.snap.At
	var suffix string
	if pf.Status != nil && pf.Status.Indicator.HasIssue() {
		suffix = " [" + pf.Status.Indicator.Label() + "]"
	}

	if pf.Data == nil {
		return fmt.Sprintf("%s: no data%s", display, suffix)
	}
	if acct.Reading.State == view.ReadingExpired || acct.Reading.State == view.ReadingError {
		problem := "error - " + format.HumanizeError(pf.Data.Error)
		if acct.Reading.State == view.ReadingExpired {
			problem = expiredMessage(pf.Data.Error)
		}
		line := fmt.Sprintf("%s: %s%s", display, problem, suffix)
		for _, grant := range acct.CreditGrants {
			line += "  " + grant.Summary()
		}
		return appendSnapshotsPlain(line, pf.Data, now)
	}

	// People see the shown windows: idle ones carry no information and stay
	// out, as in the tray and the table. Machine output keeps every window.
	shown := acct.ShownWindows()
	parts := make([]string, 0, len(shown))
	for _, window := range shown {
		parts = append(parts, plainWindow(window))
	}

	prefix := ""
	if acct.Reading.State == view.ReadingStale {
		prefix = fmt.Sprintf("stale (updated %s) - ", pf.Data.FetchedAt.Format("15:04"))
	}
	if resetSummary := resetCreditSummary(pf.Data, now); resetSummary != "" {
		parts = append(parts, resetSummary)
	}
	if announced := announcementSummary(pf.Data, now); announced != "" {
		parts = append(parts, announced)
	}
	for _, grant := range acct.CreditGrants {
		parts = append(parts, grant.Summary())
	}
	for _, balance := range pf.Data.Balances {
		label := balance.DisplayName
		if label == "" {
			label = balance.Name
		}
		parts = append(parts, fmt.Sprintf("%s: %.2f remaining", label, balance.Remaining))
	}
	for _, pool := range pf.Data.CreditPools {
		parts = append(parts, fmt.Sprintf("%s: %s", pool.Name, pool.Summary(now)))
	}
	return fmt.Sprintf("%s: %s%s%s", display, prefix, strings.Join(parts, "  "), suffix)
}

// plainWindow is one window of the plain line. Its states come from the
// snapshot, including for a window with no reset or only money.
func plainWindow(w view.Window) string {
	label := plainWindowLabel(w)
	out := w.Exhaustion.State == view.ExhaustionOut
	if w.Money != "" && !w.Reset.Known && w.Reset.Policy == "" {
		// A money cap reads best as money; the percentage only repeats it.
		line := label + ": " + w.Money
		if out {
			line += " · out now"
		}
		return line
	}
	var detail string
	switch {
	case w.Reset.Known && w.Reset.In > 0:
		detail = fmt.Sprintf("resets %s, %s", format.FormatDuration(w.Reset.In), plainIndicator(w))
	case w.Reset.Known:
		detail = "reset passed, " + plainIndicator(w)
	case w.Reset.Policy != "":
		detail = w.Reset.Policy
	default:
		detail = "reset not reported"
	}
	if !w.Reset.Known {
		detail += ", " + w.ForecastWords()
		if out {
			detail += ", out now"
		}
	}
	return fmt.Sprintf("%s: %s (%s)%s", label, format.Percent(w.Used), detail, moneySuffix(w.Money))
}

// plainIndicator is a window's forecast, with its run-out note when it has one.
func plainIndicator(w view.Window) string {
	base, note := w.ForecastWords(), w.ExhaustionWords()
	if note == "" {
		return base
	}
	return fmt.Sprintf("%-*s · %s", plainNoteWidth, base, note)
}

func moneySuffix(money string) string {
	if money != "" {
		return " [" + money + "]"
	}
	return ""
}

func plainWindowLabel(w view.Window) string {
	if w.Name == "extra" || (w.Name == "weekly" && w.Label == "7d") {
		return w.Label
	}
	return w.Name
}

// resetCreditSummary is the shared reset row. A stale live inventory is
// hidden; a browser snapshot is a dated observation and stays visible.
func resetCreditSummary(data *provider.UsageData, now time.Time) string {
	if data == nil || data.ResetCredits == nil || (data.Stale && !data.ResetCredits.Snapshot) {
		return ""
	}
	return data.ResetCredits.Summary(now)
}

// announcementSummary is the announced-reset line. Attachment has already
// decided that it bears on this account; the empty string means it does not.
func announcementSummary(data *provider.UsageData, now time.Time) string {
	if data == nil {
		return ""
	}
	return data.AnnouncedReset.Summary(now)
}

func appendSnapshotLines(lines []string, data *provider.UsageData, layout tableLayout, now time.Time) []string {
	if summary := resetCreditSummary(data, now); summary != "" && data.ResetCredits.Snapshot {
		lines = append(lines, layout.span("", summary))
	}
	for _, pool := range data.CreditPools {
		lines = append(lines, layout.span("", pool.Name+": "+pool.Summary(now)))
	}
	return lines
}

func appendSnapshotsPlain(line string, data *provider.UsageData, now time.Time) string {
	if summary := resetCreditSummary(data, now); summary != "" && data.ResetCredits.Snapshot {
		line += "  " + summary
	}
	for _, pool := range data.CreditPools {
		line += "  " + pool.Name + ": " + pool.Summary(now)
	}
	return line
}

// MultiProviderOutput handles displaying data from multiple providers.
type MultiProviderOutput struct {
	PollInterval int
	Providers    []ProviderFormatter
}

// HideUnavailable removes auto-detected providers that have not produced
// useful usage data. Explicitly enabled providers remain visible so setup
// errors are actionable.
func (m *MultiProviderOutput) HideUnavailable() {
	filtered := make([]ProviderFormatter, 0, len(m.Providers))
	for _, pf := range m.Providers {
		if !provider.ShouldShowInPrimaryUI(pf.Data, false, pf.ExplicitlyEnabled) {
			continue
		}
		filtered = append(filtered, pf)
	}
	m.Providers = filtered
}

func (m *MultiProviderOutput) PrintColor() {
	for _, line := range m.buildView(time.Now()).colorTable() {
		fmt.Println(line)
	}
}

// colorTable is the status table of one snapshot, one line per output line.
// Columns are sized to the widest row of every source, so the table lines up.
func (v outputView) colorTable() []string {
	layout := newTableLayout()
	hasRows := false
	for _, e := range v.entries {
		layout.provider = max(layout.provider, cellWidth(v.display(e.key)))
		for _, row := range shownRows(v.account(e.key)) {
			if row.Line == "" {
				hasRows = true
				layout.fit(rowCells(row))
			}
		}
	}
	var out []string
	if hasRows {
		out = append(out, dim+layout.line(tableHeader, cellWidth(tableHeader[2]), nil)+reset)
	}
	for i := range v.entries {
		lines := v.colorLines(i, layout)
		out = append(out, lines...)
		if i < len(v.entries)-1 && len(lines) > 0 {
			out = append(out, "")
		}
	}
	return out
}

func (m *MultiProviderOutput) PrintPlain() {
	v := m.buildView(time.Now())
	for i := range v.entries {
		fmt.Println(v.plainLine(i))
	}
}

func (m *MultiProviderOutput) StatusLineSummary() string {
	return m.buildView(time.Now()).statusLine()
}

// statusLine is the statusline of one snapshot.
func (v outputView) statusLine() string {
	acct, window, ok := v.worst()
	if !ok {
		return "CM no quota data"
	}
	display := v.display(acct.Key)

	if acct.Reading.State != view.ReadingFresh && window.Forecast.State == view.ForecastStale {
		return fmt.Sprintf("CM~ %s %s last %s %s", display, window.Name, format.Percent(window.Used), acct.Reading.State)
	}

	readingNote := ""
	if acct.Reading.State == view.ReadingStale {
		readingNote = " (last good " + acct.Reading.ObservedAt.Local().Format("15:04") + ")"
	}

	known := window.Forecast.State == view.ForecastKnown
	outNow := window.Exhaustion.State == view.ExhaustionOut
	prefix := "CM"
	switch {
	case outNow || (known && window.Forecast.Pct >= 100):
		prefix = "CM!"
	case (known && window.Forecast.Pct >= 90) || window.Forecast.State == view.ForecastStale:
		prefix = "CM~"
	}

	if !known {
		indicator := window.ForecastWords()
		line := fmt.Sprintf("%s %s %s current %s %s", prefix, display, window.Name, format.Percent(window.Used), indicator)
		if outNow {
			line += " out now"
		}
		return line + readingNote
	}
	resetIn := format.FormatDuration(window.Reset.At.Sub(v.snap.At))
	line := fmt.Sprintf("%s %s %s est. %s reset %s", prefix, display, window.Name, format.Percent(window.Forecast.Pct), resetIn)
	switch window.Exhaustion.State {
	case view.ExhaustionOut:
		line += " out now"
	case view.ExhaustionRunsOut:
		line += " out " + format.FormatDuration(window.Exhaustion.RunsOutIn)
	}
	return line + readingNote
}

// AgentSummary returns a token-efficient but precise status for AI agents.
// It keeps exact seconds near boundaries so a tiny slice of a 7-day window is
// still visible to the agent. Its states come from the shared snapshot.
func (m *MultiProviderOutput) AgentSummary() string {
	return m.buildView(time.Now()).agentLine()
}

// agentLine is the agent status of one snapshot.
func (v outputView) agentLine() string {
	acct, window, ok := v.worst()
	if !ok {
		// Reset observations are independent of Claude usage: keep them visible
		// when usage is unavailable.
		extra := ""
		if resets := v.agentResetCreditSummaries(); len(resets) > 0 {
			extra += "; reset_credits=[" + strings.Join(resets, " | ") + "]"
		}
		if pools := v.agentCreditPoolSummaries(); len(pools) > 0 {
			extra += "; api_credits=[" + strings.Join(pools, " | ") + "]"
		}
		if grants := v.agentCreditGrantSummaries(); len(grants) > 0 {
			extra += "; credit_grants=[" + strings.Join(grants, " | ") + "]"
		}
		if extra != "" {
			return "Quota: no active quota data" + extra + "."
		}
		return "Quota: no active quota data. Run `clawmeter providers` if setup may be incomplete."
	}

	parts := []string{fmt.Sprintf("Quota: worst=%s %s", v.display(acct.Key), window.Name)}
	parts = append(parts, agentWindowFields(acct, window, true)...)
	if quotas := v.agentQuotaSummaries(); len(quotas) > 0 {
		parts = append(parts, "quotas=["+strings.Join(quotas, " | ")+"]")
	}
	if resets := v.agentResetCreditSummaries(); len(resets) > 0 {
		parts = append(parts, "reset_credits=["+strings.Join(resets, " | ")+"]")
	}
	if pools := v.agentCreditPoolSummaries(); len(pools) > 0 {
		parts = append(parts, "api_credits=["+strings.Join(pools, " | ")+"]")
	}
	if grants := v.agentCreditGrantSummaries(); len(grants) > 0 {
		parts = append(parts, "credit_grants=["+strings.Join(grants, " | ")+"]")
	}

	return strings.Join(parts, "; ") + "."
}

func (v outputView) agentCreditGrantSummaries() []string {
	var out []string
	for _, acct := range v.snap.Accounts {
		for _, grant := range acct.CreditGrants {
			fields := []string{acct.Label + " · " + grant.Label, "scope=" + grant.Scope, "currency=" + grant.Currency, "state=" + grant.State}
			for _, amount := range []struct {
				name  string
				value *float64
			}{{"used", grant.Used}, {"limit", grant.Limit}, {"remaining", grant.Remaining}, {"last_observed_remaining", grant.LastObservedRemaining}, {"used_pct", grant.Utilization}} {
				if amount.value != nil {
					fields = append(fields, amount.name+"="+strconv.FormatFloat(*amount.value, 'f', -1, 64))
				}
			}
			if !grant.ExpiresAt.IsZero() {
				fields = append(fields, "expires_at="+grant.ExpiresAt.Format(time.RFC3339))
			}
			if !acct.Reading.ObservedAt.IsZero() {
				fields = append(fields, "observed_at="+acct.Reading.ObservedAt.Format(time.RFC3339))
			}
			out = append(out, strings.Join(fields, " "))
		}
	}
	return out
}

func (v outputView) agentResetCreditSummaries() []string {
	type resetSummary struct {
		text      string
		expiresAt time.Time
		hasExpiry bool
		provider  string
		count     int
	}
	now := v.snap.At
	summaries := make([]resetSummary, 0)
	for _, e := range v.entries {
		pf := e.pf
		if pf.Data == nil || pf.Data.ResetCredits == nil || (pf.Data.Stale && !pf.Data.ResetCredits.Snapshot) {
			continue
		}
		count := pf.Data.ResetCredits.DisplayCount(now)
		if count <= 0 {
			continue
		}
		countField := fmt.Sprintf("available=%d", count)
		if pf.Data.ResetCredits.Snapshot {
			countField = fmt.Sprintf("observed_count=%d snapshot=true", count)
		}
		display := v.display(e.key)
		fields := []string{display, countField}
		expiresAt, hasExpiry := pf.Data.ResetCredits.EarliestExpiry(now)
		if hasExpiry {
			fields = append(fields,
				"earliest_expires_at="+expiresAt.Local().Format(time.RFC3339),
				"earliest_expires_in="+formatExactDuration(expiresAt.Sub(now)),
			)
		}
		if !pf.Data.ResetCredits.FetchedAt.IsZero() {
			field := "checked_at=" + pf.Data.ResetCredits.FetchedAt.Local().Format(time.RFC3339)
			if pf.Data.ResetCredits.Snapshot {
				field = "last_observed_at=" + pf.Data.ResetCredits.FetchedAt.Local().Format(time.RFC3339)
			}
			fields = append(fields, field)
		}
		summaries = append(summaries, resetSummary{
			text:      strings.Join(fields, " "),
			expiresAt: expiresAt,
			hasExpiry: hasExpiry,
			provider:  display,
			count:     count,
		})
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		a, b := summaries[i], summaries[j]
		if a.hasExpiry != b.hasExpiry {
			return a.hasExpiry
		}
		if a.hasExpiry && !a.expiresAt.Equal(b.expiresAt) {
			return a.expiresAt.Before(b.expiresAt)
		}
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		return a.count > b.count
	})
	out := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, summary.text)
	}
	return out
}

// agentCreditPoolSummaries lists prepaid API credit pools with exact amounts
// in major currency units, so agents can budget API spend.
func (v outputView) agentCreditPoolSummaries() []string {
	now := v.snap.At
	out := make([]string, 0)
	for _, e := range v.entries {
		pf := e.pf
		display := v.display(e.key)
		if pf.Data == nil {
			continue
		}
		for _, pool := range pf.Data.CreditPools {
			money := func(minor float64) string { return strconv.FormatFloat(minor/100, 'f', 2, 64) }
			freshness := []string{fmt.Sprintf("live=%t", pool.IsLive(now))}
			if !pool.IsLive(now) {
				freshness = append(freshness, "updated_at="+pool.ObservedAt.Local().Format(time.RFC3339))
			}
			if !pool.ExpiredSinceCheck.IsZero() {
				// The saved balance no longer holds: never offer it as budget.
				out = append(out, strings.Join(append([]string{
					display + " · " + pool.Name,
					"status=expired_since_check",
					"balance=unknown",
					"expired_at=" + pool.ExpiredSinceCheck.Local().Format(time.RFC3339),
					"last_observed_at=" + pool.ObservedAt.Local().Format(time.RFC3339),
					"action=check_again",
				}, freshness...), " "))
				continue
			}
			fields := []string{
				display + " · " + pool.Name,
				"currency=" + pool.Currency,
				"balance=" + money(float64(pool.Balance)),
			}
			if pool.Balance < 0 {
				fields = append(fields, "status=unpaid", "action=add_funds")
			}
			if pool.MonthSpend != nil {
				fields = append(fields, "spent_this_month="+money(float64(*pool.MonthSpend)))
			}
			fields = append(fields,
				"burn_per_day="+money(pool.BurnPerDay),
				fmt.Sprintf("burn_basis_days=%d", pool.BurnDays),
			)
			if !pool.ExpiresAt.IsZero() {
				fields = append(fields,
					"expires_at="+pool.ExpiresAt.Local().Format(time.RFC3339),
					"expires_in="+formatExactDuration(clampDuration(pool.ExpiresAt.Sub(now))),
					"expiring_amount="+money(float64(pool.Expiring)),
					"projected_spend_before_expiry="+money(float64(pool.ProjectedSpend)),
					"projected_unused_at_expiry="+money(float64(pool.ProjectedUnused)),
				)
				if !pool.RunsOutAt.IsZero() {
					fields = append(fields, "projected_runs_out_at="+pool.RunsOutAt.Local().Format(time.RFC3339))
				}
			}
			fields = append(fields, freshness...)
			fields = append(fields, "last_observed_at="+pool.ObservedAt.Local().Format(time.RFC3339),
				fmt.Sprintf("snapshot=%t", pool.ObservationSource != "extension"))
			out = append(out, strings.Join(fields, " "))
		}
	}
	return out
}

func clampDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d.Round(time.Second)
}

func formatExactDuration(d time.Duration) string {
	d = clampDuration(d)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int64(d.Seconds()))
	}
	return format.FormatDuration(d)
}

func formatPrecisePct(pct float64) string {
	absPct := math.Abs(pct)
	nearBoundary := math.Abs(pct-90) <= 1 || math.Abs(pct-100) <= 1
	var s string
	switch {
	case absPct > 0 && absPct < 1:
		s = fmt.Sprintf("%.2f", pct)
	case nearBoundary:
		s = fmt.Sprintf("%.2f", pct)
	default:
		s = fmt.Sprintf("%.1f", pct)
	}
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s + "%"
}

func roundPct(pct float64) float64 {
	return math.Round(pct*100) / 100
}

// JSONSchemaVersion identifies the --json output contract. Consumers (e.g.
// waspflow's quota-observation envelope) pin on this instead of shape-guessing;
// bump it on any breaking change to the JSON structure.
const JSONSchemaVersion = 1

// JSONOutput represents the JSON structure for multi-provider output.
type JSONOutput struct {
	SchemaVersion int                            `json:"schema_version"`
	Providers     map[string]*ProviderJSONOutput `json:"providers"`
	FetchedAt     time.Time                      `json:"fetched_at,omitempty"`
	Cache         *CacheInfo                     `json:"cache,omitempty"`
	// View is the shared snapshot this output was built from. Added in v1.
	View *JSONView `json:"view,omitempty"`
}

// ProviderJSONOutput is the JSON representation for a single provider.
type ProviderJSONOutput struct {
	Usage    *provider.UsageData       `json:"usage,omitempty"`
	Sources  []JSONSourceOutput        `json:"sources,omitempty"`
	Forecast *JSONForecast             `json:"forecast,omitempty"`
	Status   *status.ProviderStatus    `json:"status,omitempty"`
	Maturity provider.ProviderMaturity `json:"maturity"`
	// View is the snapshot of the source that Usage describes. It is absent
	// for a family with several sources and no default source.
	View *JSONAccountView `json:"view,omitempty"`
}

type JSONSourceOutput struct {
	Source   JSONSourceIdentity     `json:"source"`
	Usage    *provider.UsageData    `json:"usage,omitempty"`
	Forecast *JSONForecast          `json:"forecast,omitempty"`
	Status   *status.ProviderStatus `json:"status,omitempty"`
	View     *JSONAccountView       `json:"view,omitempty"`
}
type JSONSourceIdentity struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// JSONForecast contains forecast data.
type JSONForecast struct {
	Windows map[string]JSONProjection `json:"windows"`
}

// JSONProjection is a single window projection.
type JSONProjection struct {
	ProjectedPct float64 `json:"projected_pct"`
	Indicator    string  `json:"indicator"`
}

// CacheInfo contains cache metadata.
type CacheInfo struct {
	Hit       bool      `json:"hit"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

// PrintJSON prints JSON output for all providers.
func (m *MultiProviderOutput) PrintJSON(cacheEntry *cache.Entry) {
	data, err := m.JSON(cacheEntry, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: json error: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

// JSON returns the --json document for m, with every state read from the
// snapshot as of now.
func (m *MultiProviderOutput) JSON(cacheEntry *cache.Entry, now time.Time) ([]byte, error) {
	return m.buildView(now).jsonDocument(cacheEntry, now)
}

// jsonDocument is the --json document of one snapshot.
func (v outputView) jsonDocument(cacheEntry *cache.Entry, now time.Time) ([]byte, error) {
	out := JSONOutput{
		SchemaVersion: JSONSchemaVersion,
		Providers:     make(map[string]*ProviderJSONOutput),
		View:          v.jsonView(),
	}

	if cacheEntry != nil {
		out.FetchedAt = cacheEntry.FetchedAt
		out.Cache = &CacheInfo{Hit: true, FetchedAt: cacheEntry.FetchedAt}
	} else {
		out.FetchedAt = now
	}

	grouped := make(map[string][]int)
	for i := range v.entries {
		family := familyOf(v.entries[i].pf)
		grouped[family] = append(grouped[family], i)
	}
	for family, group := range grouped {
		sort.SliceStable(group, func(a, b int) bool {
			return v.entries[group[a]].pf.SourceID < v.entries[group[b]].pf.SourceID
		})
		if len(group) > 1 {
			base := &ProviderJSONOutput{Maturity: provider.GetMaturity(family)}
			for _, i := range group {
				pf := *v.entries[i].pf
				src := makeJSONSource(pf, v.account(v.entries[i].key))
				src.View = v.jsonAccount(v.entries[i].key)
				base.Sources = append(base.Sources, src)
				if pf.SourceID == "default" {
					base.Usage, base.Forecast, base.Status = legacyUsage(pf.Data), src.Forecast, pf.Status
					base.View = src.View
				}
			}
			out.Providers[family] = base
			continue
		}
		i := group[0]
		pf := *v.entries[i].pf
		if pf.Data == nil {
			// No reading yet: the object carries only the view, whose reading
			// state is unavailable, so the account is not mistaken for zero use.
			out.Providers[family] = &ProviderJSONOutput{Maturity: provider.GetMaturity(family), View: v.jsonAccount(v.entries[i].key)}
			continue
		}

		providerOut := &ProviderJSONOutput{
			Usage:    legacyUsage(pf.Data),
			Maturity: provider.GetMaturity(family),
			View:     v.jsonAccount(v.entries[i].key),
		}

		providerOut.Forecast = forecastFor(v.account(v.entries[i].key))

		if pf.Status != nil {
			providerOut.Status = pf.Status
		}

		out.Providers[family] = providerOut
	}

	return json.MarshalIndent(out, "", "  ")
}

func makeJSONSource(pf ProviderFormatter, acct *view.Account) JSONSourceOutput {
	return JSONSourceOutput{Source: JSONSourceIdentity{ID: pf.SourceID, Label: pf.SourceLabel}, Usage: pf.Data, Forecast: forecastFor(acct), Status: pf.Status}
}

func legacyUsage(data *provider.UsageData) *provider.UsageData {
	if data == nil {
		return nil
	}
	copy := data.Clone()
	if copy.SourceID == "default" {
		copy.SourceID = ""
		copy.SourceLabel = ""
	}
	return copy
}

func forecastFor(acct *view.Account) *JSONForecast {
	if acct == nil {
		return nil
	}
	result := &JSONForecast{Windows: make(map[string]JSONProjection)}
	for _, window := range acct.Windows {
		if !window.Reset.Known {
			continue
		}
		projection := JSONProjection{Indicator: "unknown"}
		switch window.Forecast.State {
		case view.ForecastKnown:
			projection.ProjectedPct = roundPct(window.Forecast.Pct)
			projection.Indicator = format.Percent(window.Forecast.Pct)
		case view.ForecastStale:
			projection.Indicator = "stale"
		}
		result.Windows[window.Name] = projection
	}
	if len(result.Windows) == 0 {
		return nil
	}
	return result
}

// Status fetches and displays usage status for all configured providers.
func Status(jsonMode, plainMode, showAll bool) int {
	output, cacheEntry, code := loadStatusOutput(showAll)
	if code != 0 {
		return code
	}
	printOutput(output, jsonMode, plainMode, cacheEntry)
	return 0
}

// StatusLine prints one compact line for statusline/prompt/tmux integrations.
func StatusLine(showAll bool) int {
	output, code := loadCachedStatusOutput(showAll)
	if code != 0 {
		return code
	}
	fmt.Println(output.StatusLineSummary())
	return 0
}

// StatusAgent prints one precise, token-efficient line for AI agents.
func StatusAgent(showAll bool) int {
	output, _, code := loadStatusOutput(showAll)
	if code != 0 {
		return code
	}
	fmt.Println(output.AgentSummary())
	return 0
}

func loadStatusOutput(showAll bool) (*MultiProviderOutput, *cache.Entry, int) {
	cfg, err := config.Load(all.SourceValidator())
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return nil, nil, 1
	}

	// Create registry and register providers
	registry := provider.NewRegistry()

	all.Register(registry, cfg)

	// Try cache first — but only if it covers everything currently
	// configured. Otherwise (e.g., a provider became configured via
	// `codex login` after the last fetch) we'd return stale-by-membership
	// data and the user would see nothing for ~60s.
	configuredNames := registry.ConfiguredNames()
	if cacheEntry, err := cache.Read(); err == nil && cacheEntry.IsValid() && cacheEntry.CoversCurrent(configuredNames, sourceRevisions(registry.GetConfigured())) && !cacheEntry.HasStaleData(configuredNames) {
		output := buildOutputFromCache(registry, cfg, cacheEntry)
		attachResetAnnouncements(output, registry, cfg, time.Now())
		if showAll {
			output.IncludeAllProviders(registry, cfg)
		} else {
			output.HideUnavailable()
		}
		return output, cacheEntry, 0
	}

	// Fetch fresh data from all configured providers
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch usage and status in parallel
	var result *provider.MultiFetchResult
	var statuses map[string]*status.ProviderStatus
	done := make(chan struct{}, 2)

	go func() {
		result = provider.FetchAllParallel(ctx, registry)

		// For providers that errored, fall back to cached data if available
		if cacheEntry, err := cache.Read(); err == nil && cacheEntry != nil {
			for name, data := range result.Results {
				if data != nil && data.Error != "" && !data.HasPresentableUsage() {
					if cached, ok := staleFallback(cacheEntry, name, data, result.SourceRevisions[name]); ok {
						result.Results[name] = cached
					}
				}
			}
		}

		_ = cache.Write(result)
		done <- struct{}{}
	}()
	go func() {
		seen := make(map[string]struct{})
		var names []string
		for _, p := range registry.GetConfigured() {
			if _, ok := seen[p.Name()]; ok {
				continue
			}
			seen[p.Name()] = struct{}{}
			names = append(names, p.Name())
		}
		statuses = status.FetchAll(ctx, names)
		done <- struct{}{}
	}()
	<-done
	<-done

	// Build output
	output := buildOutputFromResult(registry, cfg, result, statuses)
	attachResetAnnouncements(output, registry, cfg, time.Now())
	if showAll {
		output.IncludeAllProviders(registry, cfg)
	} else {
		output.HideUnavailable()
	}

	return output, nil, 0
}

// resetFeedClient builds the codex-resets.com client. Tests replace it so
// that no request leaves the test process.
var resetFeedClient = resetfeed.Default

// attachResetAnnouncements shows an announced Codex reset on each account it
// bears on. It runs only when the user opted in and a Codex source is
// configured. It changes display data only, never quota values or exit codes.
func attachResetAnnouncements(output *MultiProviderOutput, registry *provider.Registry, cfg *config.Config, now time.Time) {
	if !resetfeed.Wanted(cfg.Settings.CodexResetAnnouncements, registry.GetConfigured()) {
		return
	}
	client, err := resetFeedClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reading, _ := client.Read(ctx)
	for i := range output.Providers {
		output.Providers[i].Data = resetfeed.Announce(output.Providers[i].Data, reading, now)
	}
}

func loadCachedStatusOutput(showAll bool) (*MultiProviderOutput, int) {
	cfg, err := config.Load(all.SourceValidator())
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return nil, 1
	}

	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	cacheEntry, err := cache.Read()
	if err != nil || cacheEntry == nil {
		return &MultiProviderOutput{}, 0
	}

	output := buildOutputFromCache(registry, cfg, cacheEntry)
	if showAll {
		output.IncludeAllProviders(registry, cfg)
	} else {
		output.HideUnavailable()
	}
	return output, 0
}

// IncludeAllProviders appends registered providers that were not fetched
// because they are unavailable, disabled, or opt-in. This makes `status --all`
// a true inventory view without polling providers that lack usable auth.
func (m *MultiProviderOutput) IncludeAllProviders(registry *provider.Registry, cfg *config.Config) {
	seen := make(map[string]struct{}, len(m.Providers))
	for _, pf := range m.Providers {
		seen[pf.Name] = struct{}{}
	}
	counts := familyCounts(registry.GetAll())
	for _, p := range registry.GetAll() {
		if !isUserFacingProviderSource(p) {
			continue
		}
		key := provider.SourceKey(p)
		if _, ok := seen[key]; ok {
			continue
		}
		m.Providers = append(m.Providers, ProviderFormatter{
			Name:        key,
			Display:     sourceDisplay(p, counts),
			DisplayName: p.DisplayName(), SourceCount: counts[p.Name()],
			Family: provider.FamilyName(p), SourceID: provider.SourceID(p), SourceLabel: provider.SourceLabel(p),
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(provider.FamilyName(p)),
		})
	}
}

// isUserFacingProviderSource hides the supplemental Claude reset source; its
// data is shown under the Claude account it is bound to.
func isUserFacingProviderSource(p provider.Provider) bool {
	return p.Name() != "claude_web"
}

func staleFallback(cacheEntry *cache.Entry, name string, current *provider.UsageData, revisions ...string) (*provider.UsageData, bool) {
	if current == nil || current.InvalidatesPriorUsage {
		return nil, false
	}
	cached, ok := cacheEntry.GetProvider(name)
	if !ok || cached == nil || !cached.HasPresentableUsage() {
		return nil, false
	}
	if !provider.UsageDataMatchesSource(cached, current.Provider, current.SourceID) {
		return nil, false
	}
	if len(revisions) > 0 {
		if !cache.SourceRevisionMatches(cacheEntry.SourceRevisions, name, revisions[0]) {
			return nil, false
		}
	}
	cached = cached.Clone()
	cached.MarkStale(current.Error)
	return cached, true
}

func staleReason(data *provider.UsageData) string {
	if data == nil || data.Warning == "" {
		return "usage unavailable"
	}
	return format.HumanizeError(data.Warning)
}

func staleSummary(data *provider.UsageData) string {
	reason := staleReason(data)
	if reason == "" || reason == "usage unavailable" {
		return "stale: showing last good data"
	}
	return "stale: showing last good data (" + reason + ")"
}

// Check fetches usage and returns an exit code: 0=healthy, 1=warning, 2=critical/expired/error.
func Check() int {
	cfg, err := config.Load(all.SourceValidator())
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 2
	}

	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	if len(registry.GetConfigured()) == 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: no providers configured\n")
		return 2
	}

	// Try cache first — but skip if a newly-configured provider has no
	// cached entry yet (same stale-by-membership concern as Status()).
	configuredNames := registry.ConfiguredNames()
	var output *MultiProviderOutput
	if cacheEntry, err := cache.Read(); err == nil && cacheEntry.IsValid() && cacheEntry.CoversCurrent(configuredNames, sourceRevisions(registry.GetConfigured())) && !cacheEntry.HasStaleData(configuredNames) {
		output = buildOutputFromCache(registry, cfg, cacheEntry)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := provider.FetchAllParallel(ctx, registry)
		if cacheEntry, err := cache.Read(); err == nil && cacheEntry != nil {
			for name, data := range result.Results {
				if data != nil && data.Error != "" && !data.HasPresentableUsage() {
					if cached, ok := staleFallback(cacheEntry, name, data, result.SourceRevisions[name]); ok {
						result.Results[name] = cached
					}
				}
			}
		}
		_ = cache.Write(result)
		output = buildOutputFromResult(registry, cfg, result, nil)
	}
	output.HideUnavailable()
	if len(output.Providers) == 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: no active providers\n")
		return 2
	}

	return statusCheckExitCode(output)
}

// statusCheckExitCode grades the same snapshot the display shows, so a
// retained reading's forecast decides the exit code as it decides the words.
func statusCheckExitCode(output *MultiProviderOutput) int {
	worstTier := 4
	v := output.buildView(time.Now())
	for _, e := range v.entries {
		if u := summaryUrgency(e.pf, v.account(e.key)); u.tier < worstTier {
			worstTier = u.tier
		}
	}

	// Map tiers to exit codes: 0,1,2 → exit 2; 3 → exit 1; 4 → exit 0
	switch {
	case worstTier <= 2:
		return 2
	case worstTier == 3:
		return 1
	default:
		return 0
	}
}

// buildOutputFromCache creates output from cached data.
func buildOutputFromCache(registry *provider.Registry, cfg *config.Config, cacheEntry *cache.Entry) *MultiProviderOutput {
	output := &MultiProviderOutput{
		PollInterval: cfg.Settings.PollInterval,
		Providers:    make([]ProviderFormatter, 0),
	}
	// Only show configured providers
	configured := registry.GetConfigured()
	counts := familyCounts(configured)
	sources := make(map[string]*provider.UsageData, len(configured))
	for _, p := range configured {
		key := provider.SourceKey(p)
		data, _ := cacheEntry.GetProvider(key)
		if !provider.UsageDataMatchesSource(data, p.Name(), provider.SourceID(p)) ||
			!cache.SourceRevisionMatches(cacheEntry.SourceRevisions, key, provider.SourceRevision(p)) {
			data = nil
		}
		if data != nil && cacheEntry.IsProviderStale(key) && data.HasPresentableUsage() {
			data = data.Clone()
			data.MarkStale("cache expired")
		}
		sources[key] = data
	}
	presented := provider.PresentResetCreditSources(sources)
	seen := make(map[string]struct{}, len(configured))
	for _, p := range configured {
		if !isUserFacingProviderSource(p) {
			continue
		}
		key := provider.SourceKey(p)
		data, ok := presented[key]
		seen[key] = struct{}{}
		if !ok && sources[key] != nil {
			continue // folded under another row, like linked API credit pools
		}
		output.Providers = append(output.Providers, ProviderFormatter{
			Name: key, Family: provider.FamilyName(p), SourceID: provider.SourceID(p), SourceLabel: provider.SourceLabel(p),
			Display:     sourceDisplay(p, counts),
			DisplayName: p.DisplayName(), SourceCount: counts[p.Name()],
			Data:              data,
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(provider.FamilyName(p)),
		})
	}
	appendPresentedTargetRows(output, registry, cfg, presented, seen, nil, counts)

	return output
}

// buildOutputFromResult creates output from fetch results.
func buildOutputFromResult(registry *provider.Registry, cfg *config.Config, result *provider.MultiFetchResult, statuses map[string]*status.ProviderStatus) *MultiProviderOutput {
	output := &MultiProviderOutput{
		PollInterval: cfg.Settings.PollInterval,
		Providers:    make([]ProviderFormatter, 0),
	}
	// Only show configured providers that have data
	configured := registry.GetConfigured()
	counts := familyCounts(configured)
	sources := make(map[string]*provider.UsageData, len(configured))
	for _, p := range configured {
		key := provider.SourceKey(p)
		data := result.Results[key]
		if !provider.UsageDataMatchesSource(data, p.Name(), provider.SourceID(p)) {
			data = nil
		}
		sources[key] = data
	}
	presented := provider.PresentResetCreditSources(sources)
	seen := make(map[string]struct{}, len(configured))
	for _, p := range configured {
		if !isUserFacingProviderSource(p) {
			continue
		}
		key := provider.SourceKey(p)
		data, ok := presented[key]
		seen[key] = struct{}{}
		if !ok {
			continue
		}
		output.Providers = append(output.Providers, ProviderFormatter{
			Name: key, Family: provider.FamilyName(p), SourceID: provider.SourceID(p), SourceLabel: provider.SourceLabel(p),
			Display:     sourceDisplay(p, counts),
			DisplayName: p.DisplayName(), SourceCount: counts[p.Name()],
			Data:              data,
			Status:            statuses[p.Name()],
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(provider.FamilyName(p)),
		})
	}
	appendPresentedTargetRows(output, registry, cfg, presented, seen, statuses, counts)

	return output
}

// appendPresentedTargetRows keeps a source-confirmed Claude reset snapshot
// visible when Claude Code currently has no usage result for that source. The
// browser observation is still attached to the named Claude account; no usage
// windows are synthesized.
func appendPresentedTargetRows(output *MultiProviderOutput, registry *provider.Registry, cfg *config.Config, presented map[string]*provider.UsageData, seen map[string]struct{}, statuses map[string]*status.ProviderStatus, counts map[string]int) {
	for key, data := range presented {
		if _, exists := seen[key]; exists || data == nil || data.Provider != "claude" || data.ResetCredits == nil || !data.ResetCredits.Snapshot {
			continue
		}
		p, ok := registry.Get(key)
		if !ok || p.Name() != "claude" || !provider.UsageDataMatchesSource(data, p.Name(), provider.SourceID(p)) {
			continue
		}
		output.Providers = append(output.Providers, ProviderFormatter{
			Name: key, Family: provider.FamilyName(p), SourceID: provider.SourceID(p), SourceLabel: provider.SourceLabel(p),
			Display:     sourceDisplay(p, counts),
			DisplayName: p.DisplayName(), SourceCount: counts[p.Name()],
			Data:              data,
			Status:            statuses[p.Name()],
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(provider.FamilyName(p)),
		})
	}
}

func familyCounts(providers []provider.Provider) map[string]int {
	counts := map[string]int{}
	for _, p := range providers {
		counts[p.Name()]++
	}
	return counts
}

func sourceRevisions(providers []provider.Provider) map[string]string {
	revisions := make(map[string]string)
	for _, p := range providers {
		if revision := provider.SourceRevision(p); revision != "" {
			revisions[provider.SourceKey(p)] = revision
		}
	}
	return revisions
}

// sourceDisplay is the name a provider shows people. It is the rule the shared
// view applies to every surface: the display name, plus the source label when
// the provider has more than one configured source.
func sourceDisplay(p provider.Provider, counts map[string]int) string {
	if counts[p.Name()] <= 1 {
		return p.DisplayName()
	}
	label := provider.SourceLabel(p)
	if label == "" {
		label = provider.SourceID(p)
	}
	return p.DisplayName() + " · " + label
}

// providerUrgency classifies a provider for ordering and check exit codes.
type providerUrgency struct {
	tier            int // 0=expired, 1=errored, 2=critical(>=100%), 3=warning(>=90%), 4=healthy
	maxProjectedPct float64
	worstWindow     string
	worstUsage      provider.UsageWindow
	worstProjection forecast.Projection
	runsOutIn       time.Duration
	runsOutEarlyBy  time.Duration
}

func classifyProvider(pf *ProviderFormatter) providerUrgency {
	if pf.Data == nil {
		if pf.ExplicitlyEnabled {
			return providerUrgency{tier: 1} // enabled but missing data => actionable setup issue
		}
		return providerUrgency{tier: 5} // passive unavailable providers belong at the bottom of --all
	}
	if pf.Data.IsExpired {
		return providerUrgency{tier: 0, maxProjectedPct: 100}
	}
	if pf.Data.Error != "" && !pf.Data.HasPresentableUsage() {
		return providerUrgency{tier: 1}
	}

	var maxPct float64
	var worstWindow string
	var worstUsage provider.UsageWindow
	var worstProjection forecast.Projection
	var hasWorstProjection bool
	var runsOutIn time.Duration
	var runsOutEarlyBy time.Duration
	stale := pf.Data.Stale
	for _, w := range pf.Data.PresentationWindows() {
		if !w.ResetsAt.IsZero() && !w.ResetsAt.After(time.Now()) {
			stale = true
			continue
		}
		if w.Utilization >= 100 && maxPct < w.Utilization {
			maxPct = w.Utilization
		}
		proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.WindowLength(w))
		if proj.ProjectedPct > maxPct {
			maxPct = proj.ProjectedPct
		}
		if !hasWorstProjection || forecast.CompareWindowRisk(w, proj, worstUsage, worstProjection) < 0 {
			worstWindow = w.Name
			worstUsage = w
			worstProjection = proj
			hasWorstProjection = true
			runsOutIn = proj.RunsOutIn
			runsOutEarlyBy = proj.RunsOutEarlyBy
		}
	}

	tier := 4
	switch {
	case maxPct >= 100:
		tier = 2
	case maxPct >= 90 || stale:
		tier = 3
	}

	return providerUrgency{
		tier:            tier,
		maxProjectedPct: maxPct,
		worstWindow:     worstWindow,
		worstUsage:      worstUsage,
		worstProjection: worstProjection,
		runsOutIn:       runsOutIn,
		runsOutEarlyBy:  runsOutEarlyBy,
	}
}

// sortProvidersByName sorts providers by the name they show, as the tray
// menu does.
func sortProvidersByName(providers []ProviderFormatter) {
	sort.SliceStable(providers, func(i, j int) bool {
		return strings.ToLower(providers[i].Display) < strings.ToLower(providers[j].Display)
	})
}

// sortProvidersByUrgency sorts providers so the most urgent appear first.
func sortProvidersByUrgency(providers []ProviderFormatter) {
	sort.SliceStable(providers, func(i, j int) bool {
		ui := classifyProvider(&providers[i])
		uj := classifyProvider(&providers[j])
		if ui.tier != uj.tier {
			return ui.tier < uj.tier
		}
		if cmp := forecast.CompareWindowRisk(ui.worstUsage, ui.worstProjection, uj.worstUsage, uj.worstProjection); cmp != 0 {
			return cmp < 0
		}
		return ui.maxProjectedPct > uj.maxProjectedPct
	})
}

// summaryState is the snapshot-based state of one account in the summary.
type summaryState struct {
	tier      int
	window    view.Window
	exhausted bool
}

// summaryUrgency uses the snapshot's forecast and run-out, including retained readings.
func summaryUrgency(pf *ProviderFormatter, acct *view.Account) summaryState {
	u := summaryState{tier: 4}
	switch {
	case pf.Data == nil:
		u.tier = 5
		if pf.ExplicitlyEnabled {
			u.tier = 1
		}
		return u
	case pf.Data.IsExpired:
		return summaryState{tier: 0}
	case pf.Data.Error != "" && !pf.Data.HasPresentableUsage():
		return summaryState{tier: 1}
	}
	var maxPct float64
	stale := acct.Reading.State != view.ReadingFresh
	for _, w := range acct.Windows {
		stale = stale || w.Forecast.State == view.ForecastStale
		if w.Forecast.State == view.ForecastKnown {
			maxPct = max(maxPct, w.Forecast.Pct)
		}
		// Use cannot fall before a reset, so a quota seen used up stays used
		// up until its reset, however old the reading.
		exhausted := w.Used >= 100 && (!w.Reset.Known || w.Reset.In > 0)
		if exhausted {
			maxPct = max(maxPct, w.Used)
		}
		// An exhausted window explains a critical account, so it is named
		// before any other.
		if u.window.Name == "" || exhausted && !u.exhausted || exhausted == u.exhausted && w.Risk < u.window.Risk {
			u.window, u.exhausted = w, exhausted
		}
	}
	switch {
	case maxPct >= 100:
		u.tier = 2
	case maxPct >= 90 || stale:
		u.tier = 3
	}
	return u
}

// printSummary prints a one-line summary before the per-provider details.
// Only shown when there are 2+ providers.
func printSummary(output *MultiProviderOutput, colorMode bool) {
	output.buildView(time.Now()).printSummary(colorMode)
}

func (v outputView) printSummary(colorMode bool) {
	if len(v.entries) < 2 {
		return
	}

	var healthy, warning, critical, errored, expired int
	var worstIdx int
	var worstU summaryState
	worstU.tier = 5 // sentinel

	for i, e := range v.entries {
		u := summaryUrgency(e.pf, v.account(e.key))
		switch u.tier {
		case 0:
			expired++
		case 1:
			errored++
		case 2:
			critical++
		case 3:
			warning++
		case 4:
			healthy++
		}
		if u.tier < worstU.tier || (u.tier == worstU.tier && u.window.Risk > 0 && (worstU.window.Risk == 0 || u.window.Risk < worstU.window.Risk)) {
			worstIdx = i
			worstU = u
		}
	}

	if healthy+warning+critical+errored+expired == 0 {
		return
	}

	var line string

	switch {
	case expired == 0 && errored == 0 && critical == 0 && warning == 0:
		line = fmt.Sprintf("All clear — %d providers healthy", healthy)

	case worstU.tier == 0:
		rest := summaryCounts(healthy, warning, critical, errored, expired-1)
		line = fmt.Sprintf("✗ %s expired", v.entries[worstIdx].pf.Display)
		if rest != "" {
			line += " — " + rest
		}

	case worstU.tier == 1:
		rest := summaryCounts(healthy, warning, critical, errored-1, expired)
		line = fmt.Sprintf("✗ %s error", v.entries[worstIdx].pf.Display)
		if rest != "" {
			line += " — " + rest
		}

	default:
		// Warning or critical — show the worst window
		windowLabel := v.entries[worstIdx].pf.Display
		if worstU.window.Name != "" {
			windowLabel += " " + worstU.window.Name
		}
		etaStr := ""
		switch worstU.window.Exhaustion.State {
		case view.ExhaustionRunsOut:
			etaStr = fmt.Sprintf(" (runs out in %s)", format.FormatDuration(worstU.window.Exhaustion.RunsOutIn))
		case view.ExhaustionOut:
			etaStr = " (out now)"
		}

		// Subtract worst from its own category for the rest count
		var rest string
		switch worstU.tier {
		case 2:
			rest = summaryCounts(healthy, warning, critical-1, errored, expired)
		case 3:
			rest = summaryCounts(healthy, warning-1, critical, errored, expired)
		default:
			rest = summaryCounts(healthy-1, warning, critical, errored, expired)
		}

		paceWord := worstU.window.ForecastWords()
		if worstU.window.Forecast.State != view.ForecastKnown && worstU.window.Name != "" {
			paceWord = fmt.Sprintf("current %s %s", format.Percent(worstU.window.Used), paceWord)
		}
		if worstU.tier == 3 && worstU.window.Name == "" {
			paceWord = "forecast stale"
		}
		line = fmt.Sprintf("⚠ %s %s%s", windowLabel, paceWord, etaStr)
		acct := v.account(v.entries[worstIdx].key)
		if acct.Reading.State == view.ReadingStale && worstU.window.Forecast.State != view.ForecastStale {
			line += " (last good " + acct.Reading.ObservedAt.Local().Format("15:04") + ")"
		}
		if rest != "" {
			line += " — " + rest
		}
	}

	if colorMode {
		var ansi string
		switch {
		case worstU.tier <= 1:
			ansi = "\033[31m"
		case worstU.tier <= 3:
			ansi = "\033[33m"
		default:
			ansi = "\033[32m"
		}
		fmt.Printf("%s%s%s\n\n", ansi, line, reset)
	} else {
		fmt.Println(line)
	}
}

// summaryCounts builds a compact "N healthy, N warning" string, omitting zero counts.
func summaryCounts(healthy, warning, critical, errored, expired int) string {
	var parts []string
	if healthy > 0 {
		parts = append(parts, fmt.Sprintf("%d healthy", healthy))
	}
	if warning > 0 {
		parts = append(parts, fmt.Sprintf("%d warning", warning))
	}
	if critical > 0 {
		parts = append(parts, fmt.Sprintf("%d critical", critical))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("%d errored", errored))
	}
	if expired > 0 {
		parts = append(parts, fmt.Sprintf("%d expired", expired))
	}
	return strings.Join(parts, ", ")
}

// printOutput prints the output in the requested format.
func printOutput(output *MultiProviderOutput, jsonMode, plainMode bool, cacheEntry *cache.Entry) {
	// Check if no providers are configured
	if len(output.Providers) == 0 {
		if jsonMode {
			fmt.Println(`{"error": "no active providers"}`)
		} else {
			fmt.Println("no active providers")
		}
		return
	}

	// JSON keeps the most urgent provider first; people read the table in
	// the same alphabetical order as the tray menu.
	if jsonMode {
		sortProvidersByUrgency(output.Providers)
		output.PrintJSON(cacheEntry)
		return
	}
	sortProvidersByName(output.Providers)
	if plainMode || !isTTY() {
		output.PrintPlain()
	} else {
		output.PrintColor()
	}
}

// SingleProviderStatus fetches status for a single provider (backward compatibility).
func SingleProviderStatus(providerName string, jsonMode, plainMode bool) int {
	return SingleProviderStatusSource(providerName, "", jsonMode, plainMode)
}

func SingleProviderStatusSource(providerName, sourceID string, jsonMode, plainMode bool) int {
	cfg, err := config.Load(all.SourceValidator())
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	family, ok := all.CanonicalName(providerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "clawmeter: unknown provider %q\n", providerName)
		return 1
	}
	providers := registry.GetFamily(family)
	counts := familyCounts(providers)
	if sourceID != "" {
		sourceID = strings.ToLower(strings.TrimSpace(sourceID))
		filtered := providers[:0]
		for _, candidate := range providers {
			if provider.SourceID(candidate) == sourceID {
				filtered = append(filtered, candidate)
			}
		}
		providers = filtered
	}
	if len(providers) == 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: source %q is not configured for provider %q\n", sourceID, family)
		return 2
	}

	configured := configuredSources(providers)
	for _, p := range providers {
		if cfg.IsProviderDisabled(provider.FamilyName(p)) {
			fmt.Fprintf(os.Stderr, "clawmeter: provider %q is disabled\n", family)
			return 2
		}
		if sourceID != "" && !p.IsConfigured() && !provider.IsEnrolledSource(p) && len(supplementalResetSources(registry, family, []provider.Provider{p})) == 0 {
			fmt.Fprintf(os.Stderr, "clawmeter: provider %q source %q is not configured\n", family, provider.SourceID(p))
			return 2
		}
	}
	// A Claude source without readable usage credentials still has a row when
	// it holds a browser reset observation, matching `clawmeter status`.
	display := make([]provider.Provider, 0, len(providers))
	for _, p := range providers {
		if p.IsConfigured() || provider.IsEnrolledSource(p) || len(supplementalResetSources(registry, family, []provider.Provider{p})) > 0 {
			display = append(display, p)
		}
	}
	providers = configured
	if len(display) == 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: provider %q has no configured sources\n", family)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fetch usage and status in parallel
	// Claude reset observations and plan-funded API credit pools are local
	// files fetched alongside their Claude sources and folded under them
	// before rendering.
	fetchSet := append(append([]provider.Provider{}, providers...), supplementalResetSources(registry, family, display)...)
	if api, ok := registry.Get(provider.ClaudeAPISourceName); ok && family == "claude" && api.IsConfigured() && !registry.IsDisabled(api) {
		fetchSet = append(fetchSet, api)
	}
	result := provider.FetchProvidersParallel(ctx, fetchSet)
	cacheEntry, _ := cache.Read()
	hadFetchError := applySourceFallbacks(providers, result, cacheEntry)
	result.Results = provider.PresentResetCreditSources(result.Results)
	var ps *status.ProviderStatus
	done := make(chan struct{}, 1)
	go func() {
		ps = status.Fetch(ctx, family)
		done <- struct{}{}
	}()
	<-done
	output := &MultiProviderOutput{PollInterval: cfg.Settings.PollInterval, Providers: make([]ProviderFormatter, 0, len(display))}
	for _, p := range display {
		output.Providers = append(output.Providers, ProviderFormatter{Name: provider.SourceKey(p), Family: family, SourceID: provider.SourceID(p), SourceLabel: provider.SourceLabel(p), Display: sourceDisplay(p, counts), DisplayName: p.DisplayName(), SourceCount: counts[p.Name()], Data: result.Results[provider.SourceKey(p)], Status: ps})
	}
	attachResetAnnouncements(output, registry, cfg, time.Now())

	if !jsonMode {
		sortProvidersByName(output.Providers)
	}
	if jsonMode {
		output.PrintJSON(nil)
	} else if plainMode || !isTTY() {
		output.PrintPlain()
	} else {
		output.PrintColor()
	}

	if hadFetchError {
		return 1
	}
	return 0
}

// supplementalResetSources returns the configured hidden reset-observation
// sources bound to the given Claude sources.
func supplementalResetSources(registry *provider.Registry, family string, sources []provider.Provider) []provider.Provider {
	if family != "claude" {
		return nil
	}
	var supplemental []provider.Provider
	for _, source := range sources {
		key := provider.ClaudeResetSourceName
		if id := provider.SourceID(source); id != "default" {
			key += ":" + id
		}
		if p, ok := registry.Get(key); ok && p.IsConfigured() {
			supplemental = append(supplemental, p)
		}
	}
	return supplemental
}

func applySourceFallbacks(providers []provider.Provider, result *provider.MultiFetchResult, cacheEntry *cache.Entry) bool {
	hadFetchError := false
	for _, p := range providers {
		key := provider.SourceKey(p)
		data := result.Results[key]
		if data != nil && data.Error != "" && !data.HasPresentableUsage() && cacheEntry != nil {
			if cached, ok := staleFallback(cacheEntry, key, data, result.SourceRevisions[key]); ok {
				result.Results[key] = cached
			}
		}
		if result.Errors[key] != nil {
			hadFetchError = true
		}
	}
	return hadFetchError
}

func configuredSources(providers []provider.Provider) []provider.Provider {
	configured := make([]provider.Provider, 0, len(providers))
	for _, p := range providers {
		if p.IsConfigured() || provider.IsEnrolledSource(p) {
			configured = append(configured, p)
		}
	}
	return configured
}

// expiredMessage avoids "expired — login expired" when the provider's error
// already says the login expired.
func expiredMessage(providerErr string) string {
	if providerErr == "" {
		return "expired"
	}
	msg := format.HumanizeError(providerErr)
	if strings.Contains(strings.ToLower(msg), "expired") {
		return msg
	}
	return "expired — " + msg
}
