package icons

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"testing"

	"github.com/tnunamak/clawmeter/internal/provider/all"
)

func TestGenerateProviderIconOutputsSquareNonBlankPNGs(t *testing.T) {
	for _, name := range all.Names() {
		for _, size := range []int{22, 32} {
			t.Run(name+"/"+itoa(size), func(t *testing.T) {
				img := decodePNG(t, GenerateProviderIcon(name, 42, size))
				bounds := img.Bounds()
				if bounds.Dx() != size || bounds.Dy() != size {
					t.Fatalf("generated bounds = %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), size, size)
				}
				if alphaBounds(img).Empty() {
					t.Fatal("generated icon is blank")
				}
			})
		}
	}
}

func TestRegisteredProvidersHaveRealLogos(t *testing.T) {
	for _, name := range all.Names() {
		if len(ProviderLogos[name]) == 0 {
			t.Fatalf("%q has no embedded provider logo", name)
		}
		for _, size := range []int{22, 32} {
			img := decodePNG(t, GenerateProviderIcon(name, 0, size))
			if alphaBounds(img).Empty() {
				t.Fatalf("%q has a blank %dpx rendered logo", name, size)
			}
		}
	}
}

func TestDarkProviderMarksHaveExplicitContrastTreatment(t *testing.T) {
	for name, data := range ProviderLogos {
		if !logoNeedsContrastPlate(data) {
			continue
		}
		if !providerLogoTreatments[name].contrastPlate {
			t.Fatalf("%q is a dark mark without explicit contrast treatment", name)
		}
	}
}

// At 22px (the typical Linux tray size) a dark mark must still have a visible
// light plate, otherwise it disappears on dark trays. This caught the previous
// design where the plate was sized so small it dropped out at tray sizes.
func TestGenerateProviderIconAddsContrastPlateAtTraySize(t *testing.T) {
	for _, name := range []string{"codex", "copilot", "openai", "openrouter"} {
		t.Run(name, func(t *testing.T) {
			img := decodePNG(t, GenerateProviderIcon(name, 12, 22))
			if !hasLightPixel(img) {
				t.Fatal("generated 22px icon is missing a light contrast plate")
			}
		})
	}
}

// The provider mark must remain the primary signal at tray size — i.e. at
// least some clearly-non-background pixels from the mark must survive after
// downscaling. We test against a colorful logo so the assertion isn't satisfied
// by the contrast plate or the Clawmeter overlay alone.
func TestProviderMarkRemainsVisibleAtTraySize(t *testing.T) {
	for _, name := range []string{"claude", "gemini", "kimi", "xai"} {
		t.Run(name, func(t *testing.T) {
			img := decodePNG(t, GenerateProviderIcon(name, 0, 22))
			if !hasNonBackgroundColoredPixel(img) {
				t.Fatalf("%q mark is not visible at 22px", name)
			}
		})
	}
}

func TestProviderFrameUsesTheNativeIconArea(t *testing.T) {
	img := decodePNG(t, GenerateProviderIcon("claude", 0, 128))
	bounds := alphaBounds(img)
	if bounds.Dx() < 110 || bounds.Dy() < 88 {
		t.Fatalf("native frame rendered too small: bounds=%v", bounds)
	}
}

func TestProviderIconsIncludeClawmeterOverlayAtTraySize(t *testing.T) {
	for _, name := range []string{"claude", "openai"} {
		t.Run(name, func(t *testing.T) {
			img := decodePNG(t, GenerateProviderIconWithMeter(name, MeterState{
				UsagePct:     95,
				ExpectedPct:  55,
				RiskPct:      120,
				ShowExpected: true,
			}, 22))
			if countRedDominantPixels(img) < 3 {
				t.Fatalf("%q tray icon does not show a visible Clawmeter overlay", name)
			}
		})
	}
}

func TestProviderIconsIncludeQuotaLabelAtTraySize(t *testing.T) {
	base := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     44,
		ExpectedPct:  20,
		RiskPct:      131,
		ShowExpected: true,
	}, 22))
	labeled := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     44,
		ExpectedPct:  20,
		RiskPct:      131,
		ShowExpected: true,
		Label:        "7D",
	}, 22))
	if countVisiblyDifferentPixels(base, labeled) < 14 {
		t.Fatal("quota label is not visible at tray size")
	}
}

func TestV10QuotaLabelUsesLegibleNativeBounds(t *testing.T) {
	for _, size := range []int{22, 32} {
		img := renderV10NativeFrame("openai", MeterState{
			UsagePct: 44, ExpectedPct: 20, ShowExpected: true, Label: "7D",
		}, size, frameThemeDark)
		without := renderV10NativeFrame("openai", MeterState{
			UsagePct: 44, ExpectedPct: 20, ShowExpected: true,
		}, size, frameThemeDark)
		bounds := visiblyDifferentBounds(without, img)
		want := image.Rect(5, 16, 16, 22)
		if size == 32 {
			want = image.Rect(10, 24, 21, 31)
		}
		if bounds != want {
			t.Fatalf("%dpx label bounds = %v, want %v", size, bounds, want)
		}
	}
}

func TestV10VisualChannelsNeverOccludeEachOther(t *testing.T) {
	for _, size := range []int{22, 32} {
		for _, theme := range []frameTheme{frameThemeDark, frameThemeLight} {
			for providerName := range frameProviderAsset {
				for _, label := range []string{"5h", "7d", "7A", "7S", "mo"} {
					meter, err := v10MeterRaster(size, theme, MeterState{
						UsagePct: 78, ExpectedPct: 40, ShowExpected: true,
					})
					if err != nil {
						t.Fatal(err)
					}
					providerLayer := image.NewNRGBA(image.Rect(0, 0, size, size))
					v10ProviderMark(providerLayer, providerName, size, theme)
					labelLayer := image.NewNRGBA(image.Rect(0, 0, size, size))
					v10WindowLabel(labelLayer, label, size, theme)
					updateLayer := image.NewNRGBA(image.Rect(0, 0, size, size))
					draw.Draw(updateLayer, frameGeometries[size].update, image.NewUniform(color.Opaque), image.Point{}, draw.Src)

					layers := []struct {
						name string
						img  image.Image
					}{{"meter", meter}, {"provider", providerLayer}, {"label", labelLayer}, {"update", updateLayer}}
					for i := 0; i < len(layers); i++ {
						for j := i + 1; j < len(layers); j++ {
							if layersOverlap(layers[i].img, layers[j].img) {
								t.Fatalf("%dpx %s %s/%s: %s occludes %s", size, theme, providerName, label, layers[i].name, layers[j].name)
							}
						}
					}
				}
			}
		}
	}
}

func TestProviderIconsIncludeUpdateBadgeAtTraySize(t *testing.T) {
	for _, size := range []int{22, 32} {
		t.Run(itoa(size), func(t *testing.T) {
			img := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
				UsagePct:        44,
				ExpectedPct:     20,
				RiskPct:         131,
				ShowExpected:    true,
				Label:           "7D",
				UpdateAvailable: true,
			}, size))

			bounds := blueDominantBounds(img)
			want := image.Rect(19, 19, 21, 21)
			if size == 32 {
				want = image.Rect(28, 28, 31, 31)
			}
			if bounds != want {
				t.Fatalf("update badge bounds = %v, want %v", bounds, want)
			}
		})
	}
}

func TestProviderFrameTreatsInvalidUsageAsZero(t *testing.T) {
	zero := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{ShowExpected: true}, 22))
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		got := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
			UsagePct: value, ExpectedPct: value, ShowExpected: true,
		}, 22))
		if math.IsNaN(value) || math.IsInf(value, -1) {
			if countVisiblyDifferentPixels(zero, got) != 0 {
				t.Fatalf("invalid usage %v did not normalize to zero", value)
			}
		}
	}
}

func TestNormalizeMeterLabelKeepsTwoAlphanumericCharacters(t *testing.T) {
	if got := normalizeMeterLabel("7d all"); got != "7D" {
		t.Fatalf("normalizeMeterLabel = %q, want 7D", got)
	}
	if got := normalizeMeterLabel("monthly"); got != "MO" {
		t.Fatalf("normalizeMeterLabel = %q, want MO", got)
	}
}

func TestPaceDeltaRingUsesRedWhenAhead(t *testing.T) {
	img := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     65,
		ExpectedPct:  30,
		ShowExpected: true,
	}, 22))
	if countRedDominantPixels(img) < 4 {
		t.Fatal("ahead-of-pace segment is too thin to read at tray size")
	}
	if countVisiblyDifferentPixels(img, decodePNG(t, GenerateProviderIcon("openai", 0, 22))) < 24 {
		t.Fatal("meter track does not add enough visible context at tray size")
	}
}

func TestPaceDeltaRingUsesGreenWhenBehind(t *testing.T) {
	behind := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     25,
		ExpectedPct:  70,
		ShowExpected: true,
	}, 64))
	ahead := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     70,
		ExpectedPct:  25,
		ShowExpected: true,
	}, 64))
	if countGreenDominantPixels(behind) <= countRedDominantPixels(behind) {
		t.Fatal("behind-pace usage should render a green delta segment")
	}
	if countRedDominantPixels(ahead) <= countGreenDominantPixels(ahead) {
		t.Fatal("ahead-of-pace usage should render a red delta segment")
	}
}

func TestExpectedPaceChangesDeltaRing(t *testing.T) {
	early := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     50,
		ExpectedPct:  15,
		RiskPct:      100,
		ShowExpected: true,
	}, 64))
	late := decodePNG(t, GenerateProviderIconWithMeter("openai", MeterState{
		UsagePct:     50,
		ExpectedPct:  90,
		RiskPct:      100,
		ShowExpected: true,
	}, 64))
	if countVisiblyDifferentPixels(early, late) < 20 {
		t.Fatal("expected pace should change the red/green delta segment")
	}
}

func TestPaceDeltaRingUsesOuterTrayEdge(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	drawClawMeterOverlay(img, MeterState{
		UsagePct:     90,
		ExpectedPct:  40,
		ShowExpected: true,
	}, 128)

	_, bandMaxR, bandCount := redDominantRadiusRange(img)
	if bandCount == 0 {
		t.Fatal("pace delta ring is missing")
	}
	if bandMaxR < meterArcOuterR-1 {
		t.Fatalf("pace delta ring does not use the outer tray-icon edge: max radius %.1f", bandMaxR)
	}
	if bandCount < 900 {
		t.Fatalf("pace delta ring is too small to read as a continuous band: %d red pixels", bandCount)
	}
}

func TestPaceDeltaRingDoesNotDependOnClippedBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	drawClawMeterOverlay(img, MeterState{
		UsagePct:     90,
		ExpectedPct:  40,
		ShowExpected: true,
	}, 128)

	bounds := img.Bounds()
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		for _, y := range []int{bounds.Min.Y, bounds.Max.Y - 1} {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0 {
				t.Fatal("pace delta ring touches the top or bottom icon edge")
			}
		}
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for _, x := range []int{bounds.Min.X, bounds.Max.X - 1} {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0 {
				t.Fatal("pace delta ring touches the left or right icon edge")
			}
		}
	}
}

func TestRingSegmentsUseFlatRadialEnds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	centerR := ringCenterR(meterArcOuterR, meterArcInnerR)
	halfWidth := ringHalfWidth(meterArcOuterR, meterArcInnerR)
	drawArcStroke(img, meterAngleForFraction(0), meterAngleForFraction(0.25), centerR, halfWidth, color.NRGBA{R: 232, G: 48, B: 58, A: 255})

	if hasOpaquePixelNearFractionAtRadius(img, 0.985, centerR, 2) {
		t.Fatal("ring segment has a rounded cap before the origin cut")
	}
	if hasOpaquePixelNearFractionAtRadius(img, 0.265, centerR, 2) {
		t.Fatal("ring segment has a rounded cap after the end cut")
	}
}

// The Clawmeter overlay must be visibly different between low and high risk so a
// glanceable user can tell safe pressure from dangerous pressure at tray size.
func TestClawmeterOverlayChangesBetweenLowAndHighUsage(t *testing.T) {
	for _, name := range []string{"openai"} {
		t.Run(name, func(t *testing.T) {
			low := decodePNG(t, GenerateProviderIcon(name, 5, 64))
			high := decodePNG(t, GenerateProviderIcon(name, 95, 64))
			changed := countVisiblyDifferentPixels(low, high)
			if changed < 120 {
				t.Fatalf("Clawmeter overlay did not change visibly: changed pixels=%d", changed)
			}
		})
	}
}

func TestPlainCrawfishFallback(t *testing.T) {
	for _, pct := range []float64{0, 25, 60, 90} {
		img := decodePNG(t, GenerateProviderIcon("jetbrains", pct, 64))
		if alphaBounds(img).Empty() {
			t.Fatalf("fallback crawfish at %.0f%% is blank", pct)
		}
	}
}

func TestNeedsContrastPlateOnlyForDarkMarks(t *testing.T) {
	openAI := decodePNG(t, ProviderOpenAI)
	if !needsContrastPlate(openAI) {
		t.Fatal("OpenAI mark should need a contrast plate")
	}

	claude := decodePNG(t, ProviderClaude)
	if needsContrastPlate(claude) {
		t.Fatal("Claude logo should not need a contrast plate")
	}
}

func TestResizeToFitPreservesSourceAspectRatio(t *testing.T) {
	copilot := decodePNG(t, ProviderCopilot)
	resized := resizeToFit(copilot, 64, 64)
	bounds := resized.Bounds()
	srcBounds := copilot.Bounds()
	srcRatio := float64(srcBounds.Dx()) / float64(srcBounds.Dy())
	dstRatio := float64(bounds.Dx()) / float64(bounds.Dy())
	if abs(srcRatio-dstRatio) > 0.05 {
		t.Fatalf("aspect ratio not preserved: src=%.3f dst=%.3f", srcRatio, dstRatio)
	}
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func alphaBounds(img image.Image) image.Rectangle {
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// hasLightPixel reports whether the icon contains any near-white opaque pixel.
// The contrast plate paints a pale circle behind the mark, so for dark marks
// we should find at least one such pixel.
func hasLightPixel(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a >= 0xb000 && r >= 0xc000 && g >= 0xc000 && b >= 0xc000 {
				return true
			}
		}
	}
	return false
}

// hasNonBackgroundColoredPixel finds an opaque pixel in the central logo area
// that isn't part of the contrast plate (near-white) or the Clawmeter overlay.
func hasNonBackgroundColoredPixel(img image.Image) bool {
	bounds := img.Bounds()
	startX := bounds.Min.X + bounds.Dx()/4
	endX := bounds.Min.X + bounds.Dx()*3/4
	startY := bounds.Min.Y + bounds.Dy()/4
	endY := bounds.Min.Y + bounds.Dy()*3/4
	for y := startY; y < endY; y++ {
		for x := startX; x < endX; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			// Skip near-white plate pixels.
			if r >= 0xd000 && g >= 0xd000 && b >= 0xd000 {
				continue
			}
			// Any other opaque pixel counts as the mark.
			return true
		}
	}
	return false
}

func countRedDominantPixels(img image.Image) int {
	bounds := img.Bounds()
	var n int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 200 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if r > 150 && r > g+40 && r > b+40 {
				n++
			}
		}
	}
	return n
}

func countGreenDominantPixels(img image.Image) int {
	bounds := img.Bounds()
	var n int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 200 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if g > 140 && g > r+35 && g > b+20 {
				n++
			}
		}
	}
	return n
}

func countBlueDominantPixels(img image.Image) int {
	bounds := img.Bounds()
	var n int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 200 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if b > 180 && b > r+45 && b > g+20 {
				n++
			}
		}
	}
	return n
}

func blueDominantBounds(img image.Image) image.Rectangle {
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 200 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if b <= 180 || b <= r+45 || b <= g+20 {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func hasOpaquePixelNearFractionAtRadius(img image.Image, fraction, radius, tolerance float64) bool {
	x, y := pointOnMeter(fraction, radius)
	bounds := img.Bounds()
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		for px := bounds.Min.X; px < bounds.Max.X; px++ {
			if math.Hypot(float64(px)-x, float64(py)-y) > tolerance {
				continue
			}
			_, _, _, a := img.At(px, py).RGBA()
			if a > 0 {
				return true
			}
		}
	}
	return false
}

func redDominantRadiusRange(img image.Image) (float64, float64, int) {
	bounds := img.Bounds()
	minR := math.Inf(1)
	maxR := 0.0
	var n int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 120 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if r <= 150 || r <= g+40 || r <= b+40 {
				continue
			}
			radius := math.Hypot(float64(x)-meterCenterX, float64(y)-meterCenterY)
			if radius < minR {
				minR = radius
			}
			if radius > maxR {
				maxR = radius
			}
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return minR, maxR, n
}

func redDominantBounds(img image.Image) image.Rectangle {
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 200 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if r <= 150 || r <= g+40 || r <= b+40 {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func greenDominantBounds(img image.Image) image.Rectangle {
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 200 {
				continue
			}
			r, g, b := int(c.R), int(c.G), int(c.B)
			if g <= 140 || g <= r+35 || g <= b+20 {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func countVisiblyDifferentPixels(a, b image.Image) int {
	aBounds := a.Bounds()
	bBounds := b.Bounds()
	if aBounds.Dx() != bBounds.Dx() || aBounds.Dy() != bBounds.Dy() {
		return 0
	}
	var n int
	for y := 0; y < aBounds.Dy(); y++ {
		for x := 0; x < aBounds.Dx(); x++ {
			ac := color.NRGBAModel.Convert(a.At(aBounds.Min.X+x, aBounds.Min.Y+y)).(color.NRGBA)
			bc := color.NRGBAModel.Convert(b.At(bBounds.Min.X+x, bBounds.Min.Y+y)).(color.NRGBA)
			if absInt(int(ac.R)-int(bc.R))+absInt(int(ac.G)-int(bc.G))+absInt(int(ac.B)-int(bc.B))+absInt(int(ac.A)-int(bc.A)) > 80 {
				n++
			}
		}
	}
	return n
}

func visiblyDifferentBounds(a, b image.Image) image.Rectangle {
	bounds := a.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ac := color.NRGBAModel.Convert(a.At(x, y)).(color.NRGBA)
			bc := color.NRGBAModel.Convert(b.At(x, y)).(color.NRGBA)
			if absInt(int(ac.R)-int(bc.R))+absInt(int(ac.G)-int(bc.G))+absInt(int(ac.B)-int(bc.B))+absInt(int(ac.A)-int(bc.A)) <= 80 {
				continue
			}
			found = true
			minX = min(minX, x)
			minY = min(minY, y)
			maxX = max(maxX, x+1)
			maxY = max(maxY, y+1)
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func layersOverlap(a, b image.Image) bool {
	bounds := a.Bounds().Intersect(b.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, aa := a.At(x, y).RGBA()
			_, _, _, ba := b.At(x, y).RGBA()
			if aa > 0 && ba > 0 {
				return true
			}
		}
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
