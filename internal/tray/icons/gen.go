package icons

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/inconsolata"
	"golang.org/x/image/math/fixed"
)

// Provider logo PNGs. Source assets are intentionally mixed: some are full-color
// marks, some are monochrome marks that need a light backing plate on dark trays.
var (
	//go:embed provider-claude.png
	ProviderClaude []byte
	//go:embed provider-gemini.png
	ProviderGemini []byte
	//go:embed provider-codex.png
	ProviderCodex []byte
	//go:embed provider-openai.png
	ProviderOpenAI []byte
	//go:embed provider-kimi.png
	ProviderKimi []byte
	//go:embed provider-openrouter.png
	ProviderOpenRouter []byte
	//go:embed provider-copilot.png
	ProviderCopilot []byte
)

// ProviderLogos maps provider name to its embedded logo PNG.
var ProviderLogos = map[string][]byte{
	"claude":     ProviderClaude,
	"openai":     ProviderOpenAI,
	"gemini":     ProviderGemini,
	"kimi":       ProviderKimi,
	"kimik2":     ProviderKimi,
	"codex":      ProviderCodex,
	"copilot":    ProviderCopilot,
	"openrouter": ProviderOpenRouter,
}

// ProviderLogoFallbacks documents registered providers that intentionally use
// the plain crawfish icon until a canonical provider logo is added.
var ProviderLogoFallbacks = map[string]string{
	"jetbrains": "no canonical tray-safe source asset in this repo yet",
	"synthetic": "test provider; plain crawfish icon is intentional",
	"zai":       "no canonical tray-safe source asset in this repo yet",
}

type logoTreatment struct {
	contrastPlate bool
}

// providerLogoTreatments encodes per-provider rendering rules. Only near-pure-
// black marks (openai, codex, copilot, openrouter) need a contrast plate so
// they don't disappear on dark trays. Colorful marks (claude, gemini, kimi)
// are left alone so their brand color survives.
var providerLogoTreatments = map[string]logoTreatment{
	"codex":      {contrastPlate: true},
	"copilot":    {contrastPlate: true},
	"openai":     {contrastPlate: true},
	"openrouter": {contrastPlate: true},
}

const (
	meterCenterX   = 64.0
	meterCenterY   = 64.0
	meterStartDeg  = -90.0
	meterSweepDeg  = 360.0
	meterArcOuterR = 61.0
	meterArcInnerR = 40.0
	meterChipR     = 40
)

// MeterState is the provider-agnostic state rendered by the Clawmeter overlay.
// UsagePct controls how many bits are lit, ExpectedPct marks where usage should
// be by now in the reset window, and RiskPct controls the lit-bit severity color.
type MeterState struct {
	UsagePct     float64
	ExpectedPct  float64
	RiskPct      float64
	ShowExpected bool
	Label        string
}

// GenerateProviderIcon composites a provider logo with the Clawmeter overlay.
// Dark monochrome provider marks get a light plate so they remain legible on
// dark tray backgrounds, but the provider logo remains the base identity layer.
func GenerateProviderIcon(providerName string, usagePct float64, size int) []byte {
	return GenerateProviderIconWithMeter(providerName, MeterState{
		UsagePct:    usagePct,
		ExpectedPct: usagePct,
		RiskPct:     usagePct,
	}, size)
}

// GenerateProviderIconWithMeter composites a provider logo with a richer
// Clawmeter overlay. The provider logo remains the base identity layer.
func GenerateProviderIconWithMeter(providerName string, meter MeterState, size int) []byte {
	return generateIcon(ProviderLogos[providerName], meter, size, providerLogoTreatments[providerName])
}

// GenerateIcon is the lower-level entry point used by callers that pass a raw
// logo blob (e.g. tests). It auto-detects whether a contrast plate is needed.
// If providerLogo is nil, returns the plain colored crawfish at usagePct.
func GenerateIcon(providerLogo []byte, usagePct float64, size int) []byte {
	return generateIcon(providerLogo, MeterState{
		UsagePct:    usagePct,
		ExpectedPct: usagePct,
		RiskPct:     usagePct,
	}, size, logoTreatment{contrastPlate: logoNeedsContrastPlate(providerLogo)})
}

func generateIcon(providerLogo []byte, meter MeterState, size int, treatment logoTreatment) []byte {
	// No provider logo: fall back to the colored crawfish.
	if providerLogo == nil {
		return plainCrawfish(meter.UsagePct, size)
	}

	logoImg, _, err := image.Decode(bytes.NewReader(providerLogo))
	if err != nil {
		return plainCrawfish(meter.UsagePct, size)
	}

	// Work at a high resolution then downscale at the end.
	workSize := 128
	dst := image.NewRGBA(image.Rect(0, 0, workSize, workSize))
	logoLayer := image.NewRGBA(dst.Bounds())
	logoArea := image.Rect(0, 0, workSize, workSize)

	// 1. A permanent light chip keeps dark marks legible on dark trays and
	// gives colorful marks a stable silhouette without modifying the source art.
	drawLogoChip(logoLayer, logoArea.Dx()/2, logoArea.Dy()/2, meterChipR)

	// 2. Provider mark as the base identity layer. The gauge must adapt to
	// this layer, not force provider logos to shrink.
	logoScale := 0.62
	if treatment.contrastPlate {
		logoScale = 0.56
	}
	logoBoxW := int(float64(logoArea.Dx()) * logoScale)
	logoBoxH := int(float64(logoArea.Dy()) * logoScale)
	logoResized := resizeToFit(logoImg, logoBoxW, logoBoxH)
	lx := logoArea.Min.X + (logoArea.Dx()-logoResized.Bounds().Dx())/2
	ly := logoArea.Min.Y + (logoArea.Dy()-logoResized.Bounds().Dy())/2
	draw.Draw(logoLayer, image.Rect(lx, ly, lx+logoResized.Bounds().Dx(), ly+logoResized.Bounds().Dy()),
		logoResized, image.Point{}, draw.Over)
	drawLayerClippedToCircle(dst, logoLayer, meterCenterX, meterCenterY, float64(meterChipR))

	// 3. Separate Clawmeter overlay. The provider logo is not baked into the
	// meter artwork; it stays as the recognizable center mark.
	drawClawMeterOverlay(dst, meter, workSize)
	drawMeterLabel(dst, meter.Label)

	return encodePNG(resize(dst, size))
}

// plainCrawfish renders the canonical crawfish in the color that matches
// usagePct. Used when the provider has no embedded logo.
func plainCrawfish(usagePct float64, size int) []byte {
	clawData := crawfishForPct(usagePct)
	clawImg, _, err := image.Decode(bytes.NewReader(clawData))
	if err != nil {
		return clawData
	}
	return encodePNG(resize(clawImg, size))
}

func crawfishForPct(usagePct float64) []byte {
	switch {
	case usagePct >= 80:
		return Red
	case usagePct >= 50:
		return Yellow
	case usagePct > 0:
		return Green
	default:
		return Gray
	}
}

func drawClawMeterOverlay(dst *image.RGBA, meter MeterState, workSize int) {
	meter = normalizeMeterState(meter)
	drawPaceDeltaRing(dst, meter)
}

func drawMeterLabel(dst *image.RGBA, label string) {
	label = normalizeMeterLabel(label)
	if label == "" {
		return
	}

	face := inconsolata.Bold8x16
	textW := font.MeasureString(face, label).Ceil()
	textH := face.Metrics().Height.Ceil()
	src := image.NewRGBA(image.Rect(0, 0, textW+2, textH+2))
	drawer := font.Drawer{
		Dst:  src,
		Src:  image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 255}),
		Face: face,
		Dot:  fixed.P(1, face.Metrics().Ascent.Ceil()+1),
	}
	drawer.DrawString(label)

	const (
		labelScale    = 6
		labelScalePct = 90
	)
	labelOffsetScale := labelScale * labelScalePct / 100
	scaledW := max(1, src.Bounds().Dx()*labelScale*labelScalePct/100)
	scaledH := max(1, src.Bounds().Dy()*labelScale*labelScalePct/100)
	scaled := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
	xdraw.NearestNeighbor.Scale(scaled, scaled.Bounds(), src, src.Bounds(), draw.Over, nil)

	x := dst.Bounds().Min.X + (dst.Bounds().Dx()-scaled.Bounds().Dx())/2
	y := dst.Bounds().Min.Y + (dst.Bounds().Dy()-scaled.Bounds().Dy())/2
	halo := color.NRGBA{R: 255, G: 255, B: 255, A: 150}
	for _, off := range []image.Point{
		{X: -7 * labelOffsetScale / labelScale, Y: 0},
		{X: 7 * labelOffsetScale / labelScale, Y: 0},
		{X: 0, Y: -7 * labelOffsetScale / labelScale},
		{X: 0, Y: 7 * labelOffsetScale / labelScale},
		{X: -5 * labelOffsetScale / labelScale, Y: -5 * labelOffsetScale / labelScale},
		{X: 5 * labelOffsetScale / labelScale, Y: -5 * labelOffsetScale / labelScale},
		{X: -5 * labelOffsetScale / labelScale, Y: 5 * labelOffsetScale / labelScale},
		{X: 5 * labelOffsetScale / labelScale, Y: 5 * labelOffsetScale / labelScale},
		{X: -3 * labelOffsetScale / labelScale, Y: -6 * labelOffsetScale / labelScale},
		{X: 3 * labelOffsetScale / labelScale, Y: -6 * labelOffsetScale / labelScale},
		{X: -3 * labelOffsetScale / labelScale, Y: 6 * labelOffsetScale / labelScale},
		{X: 3 * labelOffsetScale / labelScale, Y: 6 * labelOffsetScale / labelScale},
	} {
		drawTextImage(dst, scaled, x+off.X, y+off.Y, halo)
	}
	ink := color.NRGBA{R: 0, G: 0, B: 0, A: 250}
	for _, off := range []image.Point{
		{X: -1, Y: 0},
		{X: 1, Y: 0},
		{X: 0, Y: -1},
		{X: 0, Y: 1},
	} {
		drawTextImage(dst, scaled, x+off.X, y+off.Y, ink)
	}
	drawTextImage(dst, scaled, x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
}

func normalizeMeterLabel(label string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(label)) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 2 {
			break
		}
	}
	return b.String()
}

func drawTextImage(dst *image.RGBA, src *image.RGBA, offX, offY int, c color.NRGBA) {
	bounds := dst.Bounds()
	srcBounds := src.Bounds()
	for y := srcBounds.Min.Y; y < srcBounds.Max.Y; y++ {
		for x := srcBounds.Min.X; x < srcBounds.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			dx := offX + x
			dy := offY + y
			if dx < bounds.Min.X || dy < bounds.Min.Y || dx >= bounds.Max.X || dy >= bounds.Max.Y {
				continue
			}
			ink := c
			ink.A = uint8(uint32(c.A) * (a >> 8) / 255)
			blendNRGBA(dst, dx, dy, ink)
		}
	}
}

func normalizeMeterState(meter MeterState) MeterState {
	meter.UsagePct = clampPct(meter.UsagePct)
	meter.ExpectedPct = clampPct(meter.ExpectedPct)
	if meter.RiskPct < 0 {
		meter.RiskPct = 0
	}
	return meter
}

func clampPct(pct float64) float64 {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func drawPaceDeltaRing(dst *image.RGBA, meter MeterState) {
	usage := clampPct(meter.UsagePct) / 100
	expected := clampPct(meter.ExpectedPct) / 100
	centerR := ringCenterR(meterArcOuterR, meterArcInnerR)
	halfWidth := ringHalfWidth(meterArcOuterR, meterArcInnerR)
	start := meterAngleForFraction(0)
	end := meterAngleForFraction(1)

	drawArcStroke(dst, start, end, centerR, halfWidth+1.8, color.NRGBA{R: 0, G: 0, B: 0, A: 100})
	drawArcStroke(dst, start, end, centerR, halfWidth, color.NRGBA{R: 245, G: 248, B: 252, A: 58})

	if usage <= 0 && expected <= 0 {
		return
	}

	neutralEnd := usage
	if meter.ShowExpected && expected > 0 {
		neutralEnd = min(usage, expected)
	}
	if neutralEnd > 0 {
		drawArcStroke(dst, start, meterAngleForFraction(neutralEnd), centerR, halfWidth, color.NRGBA{R: 214, G: 221, B: 228, A: 210})
	}
	if !meter.ShowExpected || expected <= 0 || usage == expected {
		return
	}
	from := min(usage, expected)
	to := max(usage, expected)
	if to <= from {
		return
	}
	drawArcStroke(dst, meterAngleForFraction(from), meterAngleForFraction(to), centerR, halfWidth, paceDeltaColor(usage-expected))
}

func paceDeltaColor(delta float64) color.NRGBA {
	if delta > 0 {
		return color.NRGBA{R: 232, G: 48, B: 58, A: 255}
	}
	return color.NRGBA{R: 64, G: 211, B: 112, A: 255}
}

func ringCenterR(outerR, innerR float64) float64 {
	return (outerR + innerR) / 2
}

func ringHalfWidth(outerR, innerR float64) float64 {
	return (outerR - innerR) / 2
}

func drawArcStroke(dst *image.RGBA, startAngle, endAngle, centerR, halfWidth float64, c color.NRGBA) {
	if endAngle <= startAngle || halfWidth <= 0 {
		return
	}
	bounds := dst.Bounds()
	maxR := centerR + halfWidth + 1
	minX := max(bounds.Min.X, int(math.Floor(meterCenterX-maxR)))
	maxX := min(bounds.Max.X-1, int(math.Ceil(meterCenterX+maxR)))
	minY := max(bounds.Min.Y, int(math.Floor(meterCenterY-maxR)))
	maxY := min(bounds.Max.Y-1, int(math.Ceil(meterCenterY+maxR)))
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px := float64(x)
			py := float64(y)
			dx := px - meterCenterX
			dy := py - meterCenterY
			r := math.Hypot(dx, dy)
			angle := normalizeAngleToStart(math.Atan2(dy, dx), startAngle)
			onArc := angle >= startAngle && angle <= endAngle && math.Abs(r-centerR) <= halfWidth
			if onArc {
				blendNRGBA(dst, x, y, c)
			}
		}
	}
}

func normalizeAngleToStart(angle, start float64) float64 {
	for angle < start {
		angle += 2 * math.Pi
	}
	for angle >= start+2*math.Pi {
		angle -= 2 * math.Pi
	}
	return angle
}

func meterAngleForFraction(fraction float64) float64 {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	return (meterStartDeg + meterSweepDeg*fraction) * math.Pi / 180
}

func pointOnMeter(fraction, radius float64) (float64, float64) {
	angle := meterAngleForFraction(fraction)
	return meterCenterX + radius*math.Cos(angle), meterCenterY + radius*math.Sin(angle)
}

func logoNeedsContrastPlate(providerLogo []byte) bool {
	if providerLogo == nil {
		return false
	}
	logoImg, _, err := image.Decode(bytes.NewReader(providerLogo))
	if err != nil {
		return false
	}
	return needsContrastPlate(logoImg)
}

// needsContrastPlate reports whether a logo is dark enough to disappear on a
// dark tray background. We only flag near-pure-black monochrome marks: any
// asset with meaningful chroma or non-trivial luma is left alone. Brand marks
// that happen to be dark-but-tinted (e.g. the Kimi mark) don't get a plate so
// the canonical artwork wins.
func needsContrastPlate(img image.Image) bool {
	bounds := img.Bounds()
	var lumaTotal, count, saturated uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			r8, g8, b8 := r>>8, g>>8, b>>8
			luma := (299*r8 + 587*g8 + 114*b8) / 1000
			lumaTotal += uint64(luma)
			count++
			maxv, minv := r8, r8
			if g8 > maxv {
				maxv = g8
			}
			if b8 > maxv {
				maxv = b8
			}
			if g8 < minv {
				minv = g8
			}
			if b8 < minv {
				minv = b8
			}
			if maxv-minv > 4 {
				saturated++
			}
		}
	}
	if count == 0 {
		return false
	}
	// Pure-black marks have ~zero chroma everywhere. Tinted dark marks have
	// chroma in enough pixels that we can leave them as-is.
	if float64(saturated)/float64(count) > 0.005 {
		return false
	}
	return lumaTotal/count < 8
}

func drawContrastPlate(dst *image.RGBA, cx, cy, radius int) {
	drawCircle(dst, cx+2, cy+3, radius+3, color.NRGBA{R: 0, G: 0, B: 0, A: 56})
	drawCircle(dst, cx, cy, radius+2, color.NRGBA{R: 0, G: 0, B: 0, A: 74})
	drawCircle(dst, cx, cy, radius, color.NRGBA{R: 246, G: 248, B: 250, A: 244})
}

func drawLogoChip(dst *image.RGBA, cx, cy, radius int) {
	drawCircle(dst, cx+2, cy+3, radius+4, color.NRGBA{R: 0, G: 0, B: 0, A: 82})
	drawCircle(dst, cx, cy, radius+2, color.NRGBA{R: 35, G: 41, B: 48, A: 68})
	drawCircle(dst, cx, cy, radius, color.NRGBA{R: 246, G: 248, B: 250, A: 246})
}

func drawLabelPill(dst *image.RGBA, x, y, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	drawRoundedRect(dst, x+1, y+2, w, h, h/2, color.NRGBA{R: 0, G: 0, B: 0, A: 96})
	drawRoundedRect(dst, x, y, w, h, h/2, color.NRGBA{R: 246, G: 248, B: 250, A: 238})
}

func drawRoundedRect(dst *image.RGBA, x, y, w, h, radius int, c color.NRGBA) {
	if radius < 0 {
		radius = 0
	}
	bounds := dst.Bounds()
	for py := y; py < y+h; py++ {
		for px := x; px < x+w; px++ {
			if px < bounds.Min.X || py < bounds.Min.Y || px >= bounds.Max.X || py >= bounds.Max.Y {
				continue
			}
			cx := px
			if cx < x+radius {
				cx = x + radius
			}
			if cx >= x+w-radius {
				cx = x + w - radius - 1
			}
			cy := py
			if cy < y+radius {
				cy = y + radius
			}
			if cy >= y+h-radius {
				cy = y + h - radius - 1
			}
			if (px-cx)*(px-cx)+(py-cy)*(py-cy) <= radius*radius {
				blendNRGBA(dst, px, py, c)
			}
		}
	}
}

func drawCircle(dst *image.RGBA, cx, cy, radius int, c color.NRGBA) {
	r2 := radius * radius
	bounds := dst.Bounds()
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			if x < bounds.Min.X || y < bounds.Min.Y || x >= bounds.Max.X || y >= bounds.Max.Y {
				continue
			}
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy > r2 {
				continue
			}
			blendNRGBA(dst, x, y, c)
		}
	}
}

func drawThickLine(dst *image.RGBA, x1, y1, x2, y2, halfWidth float64, c color.NRGBA) {
	if halfWidth <= 0 {
		return
	}
	minX := int(math.Floor(min(x1, x2) - halfWidth - 1))
	maxX := int(math.Ceil(max(x1, x2) + halfWidth + 1))
	minY := int(math.Floor(min(y1, y2) - halfWidth - 1))
	maxY := int(math.Ceil(max(y1, y2) + halfWidth + 1))
	bounds := dst.Bounds()
	if minX < bounds.Min.X {
		minX = bounds.Min.X
	}
	if minY < bounds.Min.Y {
		minY = bounds.Min.Y
	}
	if maxX >= bounds.Max.X {
		maxX = bounds.Max.X - 1
	}
	if maxY >= bounds.Max.Y {
		maxY = bounds.Max.Y - 1
	}

	dx := x2 - x1
	dy := y2 - y1
	len2 := dx*dx + dy*dy
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px := float64(x)
			py := float64(y)
			t := 0.0
			if len2 > 0 {
				t = ((px-x1)*dx + (py-y1)*dy) / len2
				t = max(0, min(1, t))
			}
			nearestX := x1 + t*dx
			nearestY := y1 + t*dy
			if math.Hypot(px-nearestX, py-nearestY) <= halfWidth {
				blendNRGBA(dst, x, y, c)
			}
		}
	}
}

func drawLayerClippedToCircle(dst *image.RGBA, src *image.RGBA, cx, cy, radius float64) {
	if radius <= 0 {
		return
	}
	bounds := dst.Bounds().Intersect(src.Bounds())
	r2 := radius * radius
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			if dx*dx+dy*dy > r2 {
				continue
			}
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			blendNRGBA(dst, x, y, c)
		}
	}
}

func drawPolygon(dst *image.RGBA, points []image.Point, c color.NRGBA) {
	if len(points) < 3 {
		return
	}
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := points[0].X, points[0].Y
	for _, p := range points[1:] {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}

	bounds := dst.Bounds()
	if minX < bounds.Min.X {
		minX = bounds.Min.X
	}
	if minY < bounds.Min.Y {
		minY = bounds.Min.Y
	}
	if maxX >= bounds.Max.X {
		maxX = bounds.Max.X - 1
	}
	if maxY >= bounds.Max.Y {
		maxY = bounds.Max.Y - 1
	}

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if pointInPolygon(x, y, points) {
				blendNRGBA(dst, x, y, c)
			}
		}
	}
}

func pointInPolygon(x, y int, points []image.Point) bool {
	inside := false
	j := len(points) - 1
	px := float64(x) + 0.5
	py := float64(y) + 0.5
	for i := range points {
		yi := float64(points[i].Y)
		yj := float64(points[j].Y)
		if (yi > py) != (yj > py) {
			xi := float64(points[i].X)
			xj := float64(points[j].X)
			xAtY := (xj-xi)*(py-yi)/(yj-yi) + xi
			if px < xAtY {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

func blendNRGBA(dst *image.RGBA, x, y int, src color.NRGBA) {
	if src.A == 0 {
		return
	}
	i := dst.PixOffset(x, y)
	dstA := uint32(dst.Pix[i+3])
	srcA := uint32(src.A)
	outA := srcA + dstA*(255-srcA)/255
	if outA == 0 {
		return
	}

	for channel, srcC := range []uint8{src.R, src.G, src.B} {
		dstC := uint32(dst.Pix[i+channel])
		outC := (uint32(srcC)*srcA + dstC*dstA*(255-srcA)/255) / outA
		dst.Pix[i+channel] = uint8(outC)
	}
	dst.Pix[i+3] = uint8(outA)
}

// resize scales an image into a square box while preserving aspect ratio.
func resize(src image.Image, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 || size <= 0 {
		return dst
	}
	width := size
	height := width * srcH / srcW
	if height > size {
		height = size
		width = height * srcW / srcH
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	offX := (size - width) / 2
	offY := (size - height) / 2
	target := image.Rect(offX, offY, offX+width, offY+height)
	xdraw.CatmullRom.Scale(dst, target, src, srcBounds, xdraw.Over, nil)
	return dst
}

func resizeToFit(src image.Image, maxWidth, maxHeight int) *image.RGBA {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 || maxWidth <= 0 || maxHeight <= 0 {
		return image.NewRGBA(image.Rect(0, 0, maxWidth, maxHeight))
	}

	width := maxWidth
	height := width * srcH / srcW
	if height > maxHeight {
		height = maxHeight
		width = height * srcW / srcH
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, srcBounds, xdraw.Over, nil)
	return dst
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
