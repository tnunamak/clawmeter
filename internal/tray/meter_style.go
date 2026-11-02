//go:build tray

package tray

import (
	"strings"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/systray"
)

var tooltipAppearanceChanged = make(chan struct{}, 1)

type tooltipImage struct{ Alt, SVG string }

// meterSpans uses the same role priority as the CLI. Ambiguous text matching
// keeps the title plain rather than coloring a provider-supplied lookalike.
func meterSpans(text string, cells []meter.Cell) ([]systray.TextSpan, string) {
	bar := meter.BarText(cells)
	if bar == "" || strings.Count(text, bar) != 1 {
		return nil, ""
	}
	before, after, _ := strings.Cut(text, bar)
	spans := []systray.TextSpan{{Text: before}}
	var key strings.Builder
	for _, cell := range cells {
		style := systray.TextNormal
		switch {
		case cell.Kind == meter.Ahead:
			style = systray.TextRed
		case cell.Behind:
			style = systray.TextGreen
		case cell.Kind == meter.Pace:
			style = systray.TextBold
		case cell.Kind == meter.Projected || cell.Kind == meter.Free:
			style = systray.TextMuted
		}
		spans = append(spans, systray.TextSpan{Text: cell.Glyph, Style: style})
		key.WriteByte(byte(style) + 1)
	}
	return append(spans, systray.TextSpan{Text: after}), key.String()
}

func setMeterMenuTitle(item *systray.MenuItem, state *menuItemState, text string, cells []meter.Cell) {
	spans, key := meterSpans(text, cells)
	if key == "" {
		setMenuItemTitle(item, state, text)
		return
	}
	if !state.initialized || state.title != text || state.titleStyle != key {
		item.SetStyledTitle(spans)
		state.title, state.titleStyle, state.initialized = text, key, true
	}
}
