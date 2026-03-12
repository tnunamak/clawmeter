//go:build tray

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sort"

	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// gaugeBar represents one provider's worst window for icon rendering.
type gaugeBar struct {
	name         string
	utilization  float64 // 0–100, how full the bar is
	projectedPct float64 // projected %, determines color
	isError      bool    // grayed out
}

var (
	colorGreen  = color.RGBA{48, 209, 88, 255}  // #30d158
	colorYellow = color.RGBA{255, 214, 10, 255} // #ffd60a
	colorRed    = color.RGBA{255, 69, 58, 255}  // #ff453a
	colorGray   = color.RGBA{99, 99, 102, 255}  // #636366
	colorTrack  = color.RGBA{255, 255, 255, 25} // subtle track
	colorBg     = color.RGBA{255, 255, 255, 20} // icon background
)

func barColor(projectedPct float64) color.RGBA {
	switch {
	case projectedPct >= 100:
		return colorRed
	case projectedPct >= 90:
		return colorYellow
	default:
		return colorGreen
	}
}

// classifyForIcon extracts the worst window per provider for icon rendering.
func classifyForIcon(results map[string]*provider.UsageData) []gaugeBar {
	var bars []gaugeBar

	for name, data := range results {
		if data == nil {
			continue
		}

		if data.IsExpired {
			bars = append(bars, gaugeBar{name: name, utilization: 100, isError: true})
			continue
		}

		if data.Error != "" && len(data.Windows) == 0 {
			bars = append(bars, gaugeBar{name: name, utilization: 0, isError: true})
			continue
		}

		// Find worst window by projected %
		var worstUtil float64
		var worstProj float64
		for _, w := range data.Windows {
			proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))
			if proj.ProjectedPct > worstProj {
				worstProj = proj.ProjectedPct
				worstUtil = w.Utilization
			}
		}

		if worstUtil > 0 || worstProj > 0 {
			bars = append(bars, gaugeBar{
				name:         name,
				utilization:  worstUtil,
				projectedPct: worstProj,
			})
		}
	}

	// Sort alphabetically for stable bar ordering
	sort.Slice(bars, func(i, j int) bool {
		return bars[i].name < bars[j].name
	})

	return bars
}

// renderGaugeIcon generates a PNG icon with vertical gauge bars.
// size is the pixel dimension (square). Typical: 22 for macOS, 16/32/64 for Linux.
func renderGaugeIcon(bars []gaugeBar, size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Rounded background fill (approximate — just fill corners with transparent)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
		}
	}

	// No providers — show a gray placeholder square
	if len(bars) == 0 {
		margin := size / 4
		for y := margin; y < size-margin; y++ {
			for x := margin; x < size-margin; x++ {
				img.SetRGBA(x, y, colorTrack)
			}
		}
		return encodePNG(img)
	}

	n := len(bars)
	margin := max(2, size/6)
	gap := max(1, size/16)
	totalW := size - margin*2
	barW := max(2, (totalW-gap*(n-1))/n)

	// Recalculate to center bars if there's leftover space
	actualW := barW*n + gap*(n-1)
	offsetX := margin + (totalW-actualW)/2

	barH := size - margin*2
	baseY := margin

	for i, bar := range bars {
		x0 := offsetX + i*(barW+gap)

		// Draw track (full height, dim)
		for y := baseY; y < baseY+barH; y++ {
			for x := x0; x < x0+barW && x < size; x++ {
				img.SetRGBA(x, y, colorTrack)
			}
		}

		// Draw fill (from bottom)
		fillH := int(float64(barH) * bar.utilization / 100)
		if fillH < 0 {
			fillH = 0
		}
		if fillH > barH {
			fillH = barH
		}
		fillY := baseY + barH - fillH

		var c color.RGBA
		if bar.isError {
			c = colorGray
		} else {
			c = barColor(bar.projectedPct)
		}

		for y := fillY; y < baseY+barH; y++ {
			for x := x0; x < x0+barW && x < size; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}

	return encodePNG(img)
}

// renderGaugeIconMultiSize generates the icon at multiple sizes (for Linux HiDPI).
func renderGaugeIconMultiSize(bars []gaugeBar, sizes []int) [][]byte {
	result := make([][]byte, len(sizes))
	for i, sz := range sizes {
		result[i] = renderGaugeIcon(bars, sz)
	}
	return result
}

func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
