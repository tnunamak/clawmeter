package icons

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"

	xdraw "golang.org/x/image/draw"
)

// Native provider marks are the hand-tuned marks supplied with the Claw Frame
// V10 design. They deliberately remain separate from the dynamic meter.
//
//go:embed provider-frame/*.png
var frameAssets embed.FS

type frameGeometry struct {
	size, supersample                     int
	left, top, right, bottom, radius      float64
	providerX, providerY                  float64
	labelY                                int
	trackWidth, commonWidth, activeWidth  float64
	terminalRadius, actualRadius          float64
	expectedTickLength, expectedTickWidth float64
	expectedHaloWidth                     float64
	update                                image.Rectangle
}

var frameGeometries = map[int]frameGeometry{
	22: {22, 16, 1.5, 1.4, 19.5, 14.8, 5.2, 10.5, 8.7, 16, 1.35, 1.58, 2.0, 0.675, 1.0, 2.35, 0.62, 1.35, image.Rect(19, 19, 21, 21)},
	32: {32, 12, 2.0, 1.8, 29.0, 22.5, 7.1, 15.5, 13.0, 24, 2.05, 2.4, 3.0, 1.025, 1.5, 3.525, 0.87, 2.025, image.Rect(28, 28, 31, 31)},
}

var frameProviderAsset = map[string]string{
	"antigravity":   "antigravity",
	"claude":        "claude",
	"codex":         "codex",
	"openai":        "openai",
	"gemini":        "gemini",
	"xai":           "grok",
	"copilot":       "copilot",
	"kimi":          "kimi",
	"kimik2":        "kimi-k2",
	"alibaba":       "alibaba-coding",
	"alibaba_token": "alibaba-token",
	"openrouter":    "openrouter",
	"jetbrains":     "jetbrains",
	"synthetic":     "synthetic",
	"zai":           "zai",
}

var frameGlyphs = map[rune][]string{
	'0': {"1111", "1001", "1001", "1001", "1111"},
	'1': {"0110", "1110", "0110", "0110", "1111"},
	'2': {"1110", "0001", "0010", "0100", "1111"},
	'3': {"1110", "0001", "0110", "0001", "1110"},
	'4': {"1001", "1001", "1111", "0001", "0001"},
	'5': {"1111", "1000", "1110", "0001", "1110"},
	'6': {"0111", "1000", "1110", "1001", "0110"},
	'7': {"1111", "0001", "0010", "0100", "0100"},
	'8': {"0110", "1001", "0110", "1001", "0110"},
	'9': {"0110", "1001", "0111", "0001", "1110"},
	'A': {"0110", "1001", "1111", "1001", "1001"},
	'S': {"0111", "1000", "0110", "0001", "1110"},
	'h': {"1000", "1000", "1110", "1001", "1001"},
	'd': {"0001", "0001", "0111", "1001", "0111"},
	'm': {"00000", "00000", "11010", "10101", "10101"},
	'o': {"0000", "0000", "0110", "1001", "0110"},
}

var (
	frameTrack    = color.NRGBA{R: 94, G: 102, B: 111, A: 242}
	frameCommon   = color.NRGBA{R: 204, G: 211, B: 218, A: 255}
	frameTerminal = color.NRGBA{R: 195, G: 203, B: 211, A: 255}
	frameTickHalo = color.NRGBA{R: 20, G: 24, B: 29, A: 255}
	frameTick     = color.NRGBA{R: 250, G: 252, B: 255, A: 255}
	frameLabel    = color.NRGBA{R: 210, G: 217, B: 224, A: 255}
	frameGreen    = color.NRGBA{R: 52, G: 211, B: 91, A: 255}
	frameRed      = color.NRGBA{R: 255, G: 69, B: 58, A: 255}
	frameBlue     = color.NRGBA{R: 55, G: 150, B: 255, A: 255}
)

type framePoint struct{ x, y float64 }

// renderProviderFrameIcon implements the native 22px/32px Claw Frame V10
// geometry. Other requested sizes use the nearest intended native rendition,
// because the operating-system tray selects from the supplied pixmaps.
func renderProviderFrameIcon(providerName string, meter MeterState, size int) image.Image {
	if _, ok := frameProviderAsset[providerName]; !ok {
		return resize(decodeProviderLogo(ProviderLogos[providerName]), size)
	}
	sourceSize := 32
	if size <= 22 {
		sourceSize = 22
	}
	native := renderNativeFrame(providerName, normalizeMeterState(meter), sourceSize)
	if size == sourceSize {
		return native
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), native, native.Bounds(), draw.Over, nil)
	return dst
}

func renderNativeFrame(providerName string, meter MeterState, size int) *image.RGBA {
	g := frameGeometries[size]
	scale := float64(g.supersample)
	high := image.NewRGBA(image.Rect(0, 0, size*g.supersample, size*g.supersample))
	path := framePath(g)

	frameStroke(high, path, scale, frameTrack, g.trackWidth)
	usage := meter.UsagePct / 100
	expected := usage
	if meter.ShowExpected {
		expected = meter.ExpectedPct / 100
	}
	lower, upper := min(usage, expected), max(usage, expected)
	if lower > 0 {
		frameStroke(high, frameSubpath(path, 0, lower), scale, frameCommon, g.commonWidth)
	}
	status := frameCommon
	if meter.ShowExpected && usage < expected {
		status = frameGreen
	}
	if meter.ShowExpected && usage > expected {
		status = frameRed
	}
	if upper > lower {
		frameStroke(high, frameSubpath(path, lower, upper), scale, status, g.activeWidth)
	}

	frameDisc(high, path[0], scale, g.terminalRadius, frameTerminal)
	frameDisc(high, path[len(path)-1], scale, g.terminalRadius, frameTerminal)
	if meter.ShowExpected {
		expectedPoint, tangent := framePointAt(path, expected)
		nx, ny := -tangent.y, tangent.x
		if math.Hypot(expectedPoint.x+nx-g.providerX, expectedPoint.y+ny-g.providerY) > math.Hypot(expectedPoint.x-nx-g.providerX, expectedPoint.y-ny-g.providerY) {
			nx, ny = -nx, -ny
		}
		start := framePoint{expectedPoint.x + nx*0.02, expectedPoint.y + ny*0.02}
		end := framePoint{expectedPoint.x + nx*g.expectedTickLength, expectedPoint.y + ny*g.expectedTickLength}
		drawThickLine(high, start.x*scale, start.y*scale, end.x*scale, end.y*scale, g.expectedHaloWidth*scale/2, frameTickHalo)
		drawThickLine(high, start.x*scale, start.y*scale, end.x*scale, end.y*scale, g.expectedTickWidth*scale/2, frameTick)
	}
	actual, _ := framePointAt(path, usage)
	frameDisc(high, actual, scale, g.actualRadius, status)

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), high, high.Bounds(), draw.Over, nil)
	frameProviderMark(dst, providerName, size, g)
	frameWindowLabel(dst, frameDisplayLabel(meter.Label), g)
	if meter.UpdateAvailable {
		draw.Draw(dst, g.update, image.NewUniform(frameBlue), image.Point{}, draw.Over)
	}
	return dst
}

func decodeProviderLogo(data []byte) image.Image {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	return img
}

func frameProviderMark(dst *image.RGBA, providerName string, size int, g frameGeometry) {
	asset := frameProviderAsset[providerName]
	data, err := frameAssets.ReadFile("provider-frame/provider-" + asset + "-" + strconv.Itoa(size) + ".png")
	if err != nil {
		return
	}
	mark, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	x := int(math.Round(g.providerX)) - mark.Bounds().Dx()/2
	y := int(math.Round(g.providerY)) - mark.Bounds().Dy()/2
	// A one-pixel dark outline preserves the dark-theme native marks on a
	// light host panel without adding a chip or changing their geometry.
	for py := 0; py < mark.Bounds().Dy(); py++ {
		for px := 0; px < mark.Bounds().Dx(); px++ {
			_, _, _, a := mark.At(px, py).RGBA()
			if a < 0x6000 {
				continue
			}
			for oy := -1; oy <= 1; oy++ {
				for ox := -1; ox <= 1; ox++ {
					if ox == 0 && oy == 0 {
						continue
					}
					dx, dy := x+px+ox, y+py+oy
					if image.Pt(dx, dy).In(dst.Bounds()) {
						blendNRGBA(dst, dx, dy, color.NRGBA{R: 16, G: 20, B: 25, A: 115})
					}
				}
			}
		}
	}
	draw.Draw(dst, image.Rect(x, y, x+mark.Bounds().Dx(), y+mark.Bounds().Dy()), mark, mark.Bounds().Min, draw.Over)
}

func frameDisplayLabel(label string) string {
	normalized := normalizeMeterLabel(label)
	switch normalized {
	case "5H":
		return "5h"
	case "7D":
		return "7d"
	case "7A", "7S":
		return normalized
	case "MO":
		return "mo"
	default:
		return ""
	}
}

func frameWindowLabel(dst *image.RGBA, label string, g frameGeometry) {
	if label == "" {
		return
	}
	width := 0
	for i, r := range label {
		if i > 0 {
			width++
		}
		width += len(frameGlyphs[r][0])
	}
	x := int(math.Round(g.providerX)) - width/2
	for _, r := range label {
		glyph := frameGlyphs[r]
		for y, row := range glyph {
			for col, on := range row {
				if on == '1' {
					blendNRGBA(dst, x+col, g.labelY+y, frameLabel)
				}
			}
		}
		x += len(glyph[0]) + 1
	}
}

func framePath(g frameGeometry) []framePoint {
	r := min(g.radius, min((g.right-g.left)/2, g.bottom-g.top))
	points := []framePoint{{g.left, g.bottom}, {g.left, g.top + r}}
	for i := 0; i < 32; i++ {
		a := math.Pi + (math.Pi/2)*float64(i)/32
		points = append(points, framePoint{g.left + r + r*math.Cos(a), g.top + r + r*math.Sin(a)})
	}
	points = append(points, framePoint{g.right - r, g.top})
	for i := 0; i < 32; i++ {
		a := 3*math.Pi/2 + (math.Pi/2)*float64(i)/32
		points = append(points, framePoint{g.right - r + r*math.Cos(a), g.top + r + r*math.Sin(a)})
	}
	points = append(points, framePoint{g.right, g.bottom})
	for i := range points {
		points[i].x = math.Round(points[i].x*2) / 2
		points[i].y = math.Round(points[i].y*2) / 2
	}
	return points
}

func framePointAt(points []framePoint, fraction float64) (framePoint, framePoint) {
	fraction = min(1, max(0, fraction))
	lengths, total := frameLengths(points)
	target := total * fraction
	for i := 0; i < len(points)-1; i++ {
		if target > lengths[i+1] && i < len(points)-2 {
			continue
		}
		segment := lengths[i+1] - lengths[i]
		t := 0.0
		if segment > 0 {
			t = (target - lengths[i]) / segment
		}
		dx, dy := points[i+1].x-points[i].x, points[i+1].y-points[i].y
		norm := math.Hypot(dx, dy)
		tangent := framePoint{}
		if norm > 0 {
			tangent = framePoint{dx / norm, dy / norm}
		}
		return framePoint{points[i].x + dx*t, points[i].y + dy*t}, tangent
	}
	return points[len(points)-1], framePoint{0, 1}
}

func frameSubpath(points []framePoint, from, to float64) []framePoint {
	if to < from {
		from, to = to, from
	}
	start, _ := framePointAt(points, from)
	end, _ := framePointAt(points, to)
	lengths, total := frameLengths(points)
	result := []framePoint{start}
	for i := 1; i < len(points)-1; i++ {
		if lengths[i] > from*total && lengths[i] < to*total {
			result = append(result, points[i])
		}
	}
	return append(result, end)
}

func frameLengths(points []framePoint) ([]float64, float64) {
	lengths := make([]float64, len(points))
	for i := 1; i < len(points); i++ {
		lengths[i] = lengths[i-1] + math.Hypot(points[i].x-points[i-1].x, points[i].y-points[i-1].y)
	}
	return lengths, lengths[len(lengths)-1]
}

func frameStroke(dst *image.RGBA, points []framePoint, scale float64, c color.NRGBA, width float64) {
	for i := 0; i < len(points)-1; i++ {
		drawThickLine(dst, points[i].x*scale, points[i].y*scale, points[i+1].x*scale, points[i+1].y*scale, width*scale/2, c)
	}
}

func frameDisc(dst *image.RGBA, p framePoint, scale, radius float64, c color.NRGBA) {
	drawFilledCircle(dst, p.x*scale, p.y*scale, radius*scale, c)
}
