package view

import "github.com/tnunamak/clawmeter/internal/meter"

// Rows lays out the account's shown windows as list rows, in provider order.
// The tray menu and the CLI table both draw these rows.
func (a Account) Rows() []meter.Row {
	shown := a.ShownWindows()
	codes := make(map[string]int, len(shown))
	for _, w := range shown {
		codes[w.Code]++
	}
	rows := make([]meter.Row, 0, len(shown))
	for _, w := range shown {
		rows = append(rows, w.Row(codes[w.Code] > 1))
	}
	return rows
}

// ShownWindows returns the windows list surfaces show, in provider order.
func (a Account) ShownWindows() []Window {
	shown := make(map[string]bool, len(a.Shown))
	for _, name := range a.Shown {
		shown[name] = true
	}
	out := make([]Window, 0, len(a.Shown))
	for _, w := range a.Windows {
		if shown[w.Name] {
			out = append(out, w)
		}
	}
	return out
}

// Row lays out one window for a list. codeShared is true when another shown
// window has the same code, so the qualifier must tell them apart.
//
// Every window's state is read from the snapshot before any layout choice, so a
// window with no reset or only money reads the same exhaustion and forecast
// states as any other. A reading that is not fresh has no forecast and no
// run-out: its forecast is stale or unknown, and its exhaustion is unknown.
func (w Window) Row(codeShared bool) meter.Row {
	out := w.Exhaustion.State == ExhaustionOut
	if w.Money != "" && !w.Reset.Known && w.Reset.Policy == "" {
		line := w.Label + ": " + w.Money
		if out {
			line += " · out now"
		}
		return meter.Row{Line: line}
	}
	row := meter.Row{Code: w.Code, Money: w.Money, Used: meter.Percent(w.Used)}
	if w.Qualifier != "" && (codeShared || w.Qualifier != "all models") {
		row.Qualifier = w.Qualifier
	}
	bar := func(projected float64) []meter.Cell {
		return meter.Bar(w.Used, projected, w.Pace, w.HasPace)
	}
	if !w.Reset.Known {
		row.Resets, row.ResetPolicy = w.Reset.Policy, true
		if row.Resets == "" {
			row.Resets = "reset not reported"
		}
		row.Bar = bar(w.Used)
		if w.Forecast.State == ForecastStale {
			row.ForecastState = "stale"
		}
		if out {
			row.RunsOut = "now"
			row.RanOutAt = w.Exhaustion.OutSince
		}
		return row
	}

	if w.Reset.In <= 0 {
		// The reset has passed: the reading says nothing about the window now.
		row.Resets, row.ResetPolicy = "reset passed", true
	} else {
		row.Resets = meter.Duration(w.Reset.In)
	}
	if w.Forecast.State == ForecastKnown {
		row.Bar = bar(w.Forecast.Pct)
		row.Forecast = meter.Percent(w.Forecast.Pct)
		row.Over = w.Forecast.Pct > 100
	} else {
		row.ForecastState = "unknown"
		if w.Forecast.State == ForecastStale {
			row.ForecastState = "stale"
		}
		row.Bar = bar(w.Used)
	}

	switch w.Exhaustion.State {
	case ExhaustionOut:
		row.RunsOut = "now"
		row.RanOutAt = w.Exhaustion.OutSince
	case ExhaustionRunsOut:
		row.RunsOut = "now"
		if w.Exhaustion.RunsOutIn > 0 {
			row.RunsOut = meter.Duration(w.Exhaustion.RunsOutIn)
		}
	}
	return row
}
