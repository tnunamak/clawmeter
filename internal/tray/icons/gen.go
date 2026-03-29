package icons

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	_ "embed"
)

// Provider logo PNGs (white on transparent, 64x64).
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

// GenerateIcon composites a provider logo with the clawmeter crawfish overlay.
// The provider logo fills the icon as the base layer.
// The crawfish is overlaid on top as a smaller gauge that fills from bottom
// based on usagePct (0-100).
// Color transitions: green (<50%), yellow (50-80%), red (>=80%).
// If providerLogo is nil, returns the plain crawfish icon at the appropriate color.
func GenerateIcon(providerLogo []byte, usagePct float64, size int) []byte {
	// Pick the colored crawfish based on severity
	var clawData []byte
	switch {
	case usagePct >= 80:
		clawData = Red
	case usagePct >= 50:
		clawData = Yellow
	case usagePct > 0:
		clawData = Green
	default:
		clawData = Gray
	}

	clawImg, _, err := image.Decode(bytes.NewReader(clawData))
	if err != nil {
		return clawData
	}

	// If no provider logo, return plain crawfish resized
	if providerLogo == nil {
		return encodePNG(resize(clawImg, size))
	}

	logoImg, _, err := image.Decode(bytes.NewReader(providerLogo))
	if err != nil {
		return encodePNG(resize(clawImg, size))
	}

	grayImg, _, err := image.Decode(bytes.NewReader(Gray))
	if err != nil {
		return encodePNG(resize(clawImg, size))
	}

	// Work at 128x128 for compositing, then resize at the end.
	workSize := 128
	dst := image.NewRGBA(image.Rect(0, 0, workSize, workSize))

	// 1. Provider logo as full-size base layer.
	// The logos are white-on-transparent — render them white on a dark circle.
	cx, cy := workSize/2, workSize/2
	bgR := workSize/2 - 2
	for y := 0; y < workSize; y++ {
		for x := 0; x < workSize; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= bgR*bgR {
				dst.Set(x, y, color.RGBA{26, 26, 46, 255})
			}
		}
	}

	// Scale logo to ~85% of the icon and center it
	logoSize := int(float64(workSize) * 0.85)
	logoResized := resize(logoImg, logoSize)
	lx := (workSize - logoSize) / 2
	ly := (workSize - logoSize) / 2
	draw.Draw(dst, image.Rect(lx, ly, lx+logoSize, ly+logoSize),
		logoResized, image.Point{}, draw.Over)

	// 2. Overlay the crawfish in the top-left as a gauge badge.
	// Bigger (~80% of icon), offset above and left of the provider logo.
	clawSize := int(float64(workSize) * 0.80)
	clawX := -8
	clawY := -12

	grayResized := resize(grayImg, clawSize)
	clawResized := resize(clawImg, clawSize)

	// Draw gray crawfish base
	for y := 0; y < clawSize; y++ {
		for x := 0; x < clawSize; x++ {
			px := clawX + x
			py := clawY + y
			if px < 0 || py < 0 || px >= workSize || py >= workSize {
				continue
			}
			c := grayResized.At(x, y)
			_, _, _, a := c.RGBA()
			if a > 0 {
				dst.Set(px, py, c)
			}
		}
	}

	// Draw colored crawfish from bottom based on fill %
	fillLine := int(float64(clawSize) * (1 - usagePct/100.0))
	for y := fillLine; y < clawSize; y++ {
		for x := 0; x < clawSize; x++ {
			px := clawX + x
			py := clawY + y
			if px < 0 || py < 0 || px >= workSize || py >= workSize {
				continue
			}
			c := clawResized.At(x, y)
			_, _, _, a := c.RGBA()
			if a > 0 {
				dst.Set(px, py, c)
			}
		}
	}

	return encodePNG(resize(dst, size))
}

// resize scales an image to size x size using nearest-neighbor.
func resize(src image.Image, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			srcX := x * srcW / size
			srcY := y * srcH / size
			dst.Set(x, y, src.At(srcBounds.Min.X+srcX, srcBounds.Min.Y+srcY))
		}
	}
	return dst
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
