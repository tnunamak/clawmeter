//go:build tray

package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"

	"fyne.io/systray"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

var currentIconName string

var iconSet = map[string][]byte{
	"green":  icons.Green,
	"yellow": icons.Yellow,
	"red":    icons.Red,
	"gray":   icons.Gray,
}

func setupIconTheme() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	base := filepath.Join(home, ".local", "share", "icons", "hicolor")

	sizes := []int{16, 22, 24, 32, 48, 64, 128}
	for name, data := range iconSet {
		for _, size := range sizes {
			dir := filepath.Join(base, fmt.Sprintf("%dx%d", size, size), "status")
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, "clawmeter-"+name+".png"), resizePNG(data, size), 0644)
		}
	}

	exec.Command("gtk-update-icon-cache", "-f", "-t", base).Run()
}

func cleanupIconTheme() {}

func setIconByName(name string, _ []byte) {
	iconName := "clawmeter-" + name
	if iconName == currentIconName {
		return
	}
	currentIconName = iconName

	// Provide multiple pixmap sizes as fallback for DEs that can't resolve
	// the icon name (e.g. user hicolor dir without index.theme).
	data := iconSet[name]
	pixmaps := make([][]byte, 0, 3)
	for _, size := range []int{16, 32, 64} {
		pixmaps = append(pixmaps, resizePNG(data, size))
	}
	systray.SetIconNameWithPixmap(iconName, pixmaps)
}

// setDynamicIcon sets the tray icon from dynamically rendered PNG data.
// On Linux, we generate a temporary icon name and provide multi-size pixmaps.
func setDynamicIcon(pngData []byte) {
	// Reset the named icon tracking so we always update
	currentIconName = ""

	// Decode the provided PNG (assumed to be the canonical size, e.g. 64px)
	// and provide re-rendered versions at standard sizes
	pixmaps := make([][]byte, 0, 3)
	for _, size := range []int{16, 32, 64} {
		pixmaps = append(pixmaps, resizePNG(pngData, size))
	}

	// Use a stable icon name with pixmap data — the pixmap is the actual
	// rendered gauge, the name is just for StatusNotifierItem protocol.
	systray.SetIconNameWithPixmap("clawmeter-gauge", pixmaps)
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
