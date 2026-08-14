// icon-sheet renders the current tray icon system for visual review.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type sample struct {
	name  string
	label string
}

var providers = []sample{
	{"antigravity", "Antigravity"},
	{"claude", "Claude"},
	{"codex", "Codex"},
	{"openai", "OpenAI"},
	{"gemini", "Gemini"},
	{"xai", "Grok"},
	{"copilot", "Copilot"},
	{"kimi", "Kimi"},
	{"kimik2", "Kimi K2"},
	{"alibaba", "Alibaba"},
	{"alibaba_token", "Alibaba Token"},
	{"openrouter", "OpenRouter"},
	{"jetbrains", "JetBrains"},
	{"synthetic", "Synthetic"},
	{"zai", "z.ai"},
	{"deepseek", "DeepSeek"},
}

type state struct {
	name  string
	meter icons.MeterState
}

var states = []state{
	{"On pace", icons.MeterState{UsagePct: 45, ExpectedPct: 45, RiskPct: 45, ShowExpected: true, Label: "7D"}},
	{"Under pace", icons.MeterState{UsagePct: 30, ExpectedPct: 65, RiskPct: 30, ShowExpected: true, Label: "7D"}},
	{"Over pace", icons.MeterState{UsagePct: 78, ExpectedPct: 40, RiskPct: 78, ShowExpected: true, Label: "7D"}},
}

func main() {
	outDir := flag.String("out", "docs/design-variations", "output directory")
	flag.Parse()
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}
	for _, size := range []int{22, 32} {
		img := renderSheet(size)
		path := filepath.Join(*outDir, fmt.Sprintf("provider-contact-sheet-real-%dpx.png", size))
		writePNG(path, img)
		if size == 32 {
			writePNG(filepath.Join(*outDir, "provider-contact-sheet.png"), scaleNearest(img, 4))
		}
	}
}

func renderSheet(iconSize int) image.Image {
	const margin, gap, labelWidth, headerHeight, rowGap = 8, 8, 108, 18, 7
	cellWidth := labelWidth + iconSize
	width := margin*2 + len(states)*cellWidth + (len(states)-1)*gap
	height := margin + headerHeight + len(providers)*(iconSize+rowGap) - rowGap + margin
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{R: 30, G: 33, B: 38, A: 255}), image.Point{}, draw.Src)

	for i, s := range states {
		x := margin + i*(cellWidth+gap)
		drawText(canvas, x, margin+12, s.name, color.NRGBA{R: 236, G: 239, B: 243, A: 255})
	}
	for row, provider := range providers {
		y := margin + headerHeight + row*(iconSize+rowGap)
		for col, s := range states {
			x := margin + col*(cellWidth+gap)
			drawText(canvas, x, y+iconSize/2+4, provider.label, color.NRGBA{R: 205, G: 210, B: 218, A: 255})
			icon := decodePNG(icons.GenerateProviderIconWithMeter(provider.name, s.meter, iconSize))
			draw.Draw(canvas, image.Rect(x+labelWidth, y, x+labelWidth+iconSize, y+iconSize), icon, image.Point{}, draw.Over)
		}
	}
	return canvas
}

func drawText(dst *image.RGBA, x, baseline int, text string, c color.NRGBA) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: basicfont.Face7x13, Dot: fixed.P(x, baseline)}
	d.DrawString(text)
}

func decodePNG(data []byte) image.Image {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		fatal(err)
	}
	return img
}

func scaleNearest(src image.Image, factor int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx()*factor, src.Bounds().Dy()*factor))
	xdraw.NearestNeighbor.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fatal(err)
	}
	fmt.Println(path)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "icon-sheet:", err)
	os.Exit(1)
}
