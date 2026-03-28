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

// GenerateIcon composites a provider logo inside the clawmeter crawfish gauge.
// The crawfish fills from bottom to top based on usagePct (0-100).
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

	// Decode the crawfish icon
	clawImg, _, err := image.Decode(bytes.NewReader(clawData))
	if err != nil {
		return clawData
	}

	// Decode the gray crawfish for unfilled portion
	grayImg, _, err := image.Decode(bytes.NewReader(Gray))
	if err != nil {
		return clawData
	}

	// If no provider logo, return plain crawfish resized
	if providerLogo == nil {
		return encodePNG(resize(clawImg, size))
	}

	// Decode provider logo
	logoImg, _, err := image.Decode(bytes.NewReader(providerLogo))
	if err != nil {
		return encodePNG(resize(clawImg, size))
	}

	// Work at 128x128 for compositing, then resize at the end
	workSize := 128

	// 1. Start with gray crawfish as background
	dst := image.NewRGBA(image.Rect(0, 0, workSize, workSize))
	grayResized := resize(grayImg, workSize)
	draw.Draw(dst, dst.Bounds(), grayResized, image.Point{}, draw.Over)

	// 2. Overlay colored crawfish from bottom, masked by fill level
	fillLine := int(float64(workSize) * (1 - usagePct/100.0))
	clawResized := resize(clawImg, workSize)
	for y := fillLine; y < workSize; y++ {
		for x := 0; x < workSize; x++ {
			c := clawResized.At(x, y)
			_, _, _, a := c.RGBA()
			if a > 0 {
				dst.Set(x, y, c)
			}
		}
	}

	// 3. Draw a dark backing circle for the provider logo
	logoSize := int(float64(workSize) * 0.42)
	cx, cy := workSize/2, workSize/2
	backingR := logoSize/2 + 3
	for y := cy - backingR; y <= cy+backingR; y++ {
		for x := cx - backingR; x <= cx+backingR; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= backingR*backingR {
				dst.Set(x, y, color.RGBA{20, 20, 35, 210})
			}
		}
	}

	// 4. Composite the provider logo centered
	logoResized := resize(logoImg, logoSize)
	lx := (workSize - logoSize) / 2
	ly := (workSize - logoSize) / 2
	draw.Draw(dst, image.Rect(lx, ly, lx+logoSize, ly+logoSize),
		logoResized, image.Point{}, draw.Over)

	// 5. Resize to target size and encode
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
