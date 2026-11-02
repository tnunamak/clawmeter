package icons

import (
	"fmt"
	"strings"

	"github.com/tnunamak/clawmeter/internal/meter"
)

// HoverMeterSVG colors the canonical cells without recalculating quota facts.
func HoverMeterSVG(bar []meter.Cell, palette TrayPalette) []byte {
	count, ticks := 0, 0
	for _, cell := range bar {
		if cell.Kind < meter.Used || cell.Kind > meter.Pace {
			return nil
		}
		if cell.Kind == meter.Pace {
			ticks++
		} else {
			count++
		}
	}
	if count != meter.BarCells || ticks > 1 {
		return nil
	}
	neutral, track, tick := "#d6dde4", "#353c44", "#f5f8fc"
	if palette == TrayPaletteLight {
		neutral, track, tick = "#59636e", "#e4e8ec", "#232930"
	}
	ahead, behind := "#ff8a80", "#81c995"
	if palette == TrayPaletteLight {
		ahead, behind = "#b3261e", "#137333"
	}
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="120" height="14" viewBox="0 0 120 14">`)
	index, pace := 0, -1
	for _, cell := range bar {
		if cell.Kind == meter.Pace {
			pace = index
			continue
		}
		x := 1 + index*12
		ink := neutral
		if cell.Kind == meter.Ahead {
			ink = ahead
		} else if cell.Behind {
			ink = behind
		}
		switch cell.Kind {
		case meter.Used, meter.Ahead:
			fmt.Fprintf(&b, `<rect x="%d" y="3" width="10" height="8" rx="0.5" fill="%s"/>`, x, ink)
		case meter.Projected:
			// Open outline and literal stipple, never a URL-backed pattern.
			fmt.Fprintf(&b, `<rect x="%d.5" y="3.5" width="9" height="7" rx="0.5" fill="%s" stroke="%s" stroke-width="1"/>`, x, track, ink)
			for _, dx := range []int{3, 5, 7} {
				for _, y := range []int{5, 8} {
					fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`, x+dx, y, ink)
				}
			}
		case meter.Free:
			edge := neutral
			if cell.Behind {
				edge = behind
			}
			fmt.Fprintf(&b, `<rect x="%d.5" y="3.5" width="9" height="7" rx="0.5" fill="%s" stroke="%s" stroke-width="1"/>`, x, track, edge)
		}
		index++
	}
	if pace >= 0 {
		// At either endpoint inset by one pixel; all geometry stays in view.
		x := max(0, min(119, pace*12))
		fmt.Fprintf(&b, `<rect x="%d" y="1" width="1" height="12" fill="%s"/>`, x, tick)
	}
	b.WriteString("</svg>")
	return []byte(b.String())
}
