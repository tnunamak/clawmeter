//go:build tray

package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"fyne.io/systray"

	"github.com/tnunamak/clawmeter/internal/tray/icons"

	_ "image/png"
)

var iconThemePath string

var iconSet = map[string][]byte{
	"green":  icons.Green,
	"yellow": icons.Yellow,
	"red":    icons.Red,
	"gray":   icons.Gray,
}

func setupIconTheme() {
	dir, err := os.MkdirTemp("", "clawmeter-icons-*")
	if err != nil {
		return
	}
	iconThemePath = dir

	// KDE's KIconLoader with addAppDir looks for icons at:
	//   {path}/hicolor/{size}x{size}/{iconname}.png
	// NOT under an apps/ subdirectory.
	indexContent := `[Icon Theme]
Name=clawmeter
Comment=Clawmeter tray icons
Directories=hicolor/16x16,hicolor/22x22,hicolor/24x24,hicolor/32x32,hicolor/48x48,hicolor/64x64,hicolor/128x128,hicolor/256x256

[hicolor/16x16]
Size=16
Type=Fixed

[hicolor/22x22]
Size=22
Type=Fixed

[hicolor/24x24]
Size=24
Type=Fixed

[hicolor/32x32]
Size=32
Type=Fixed

[hicolor/48x48]
Size=48
Type=Fixed

[hicolor/64x64]
Size=64
Type=Fixed

[hicolor/128x128]
Size=128
Type=Fixed

[hicolor/256x256]
Size=256
Type=Fixed
`
	os.WriteFile(filepath.Join(dir, "index.theme"), []byte(indexContent), 0644)

	sizes := []int{16, 22, 24, 32, 48, 64, 128, 256}
	for name, data := range iconSet {
		for _, size := range sizes {
			sizeDir := filepath.Join(dir, "hicolor", fmt.Sprintf("%dx%d", size, size))
			os.MkdirAll(sizeDir, 0755)
			resized := resizePNG(data, size)
			os.WriteFile(filepath.Join(sizeDir, "clawmeter-"+name+".png"), resized, 0644)
		}
	}
}

func cleanupIconTheme() {
	if iconThemePath != "" {
		os.RemoveAll(iconThemePath)
	}
}

func setIconByName(name string, data []byte) {
	if iconThemePath != "" {
		systray.SetIconName(iconThemePath, "clawmeter-"+name)
		return
	}
	systray.SetIcon(data)
}

func resizePNG(data []byte, size int) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}

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

	var buf bytes.Buffer
	png.Encode(&buf, dst)
	return buf.Bytes()
}
