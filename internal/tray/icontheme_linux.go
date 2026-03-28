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

func setIconDynamic(providerName string, pct float64, data128 []byte) {
	// Use a unique icon name per provider so KDE picks up the change
	iconName := fmt.Sprintf("clawmeter-dyn-%s", providerName)
	if providerName == "" {
		iconName = "clawmeter-dyn-none"
	}

	// Write to icon theme at multiple sizes
	home, err := os.UserHomeDir()
	if err == nil {
		base := filepath.Join(home, ".local", "share", "icons", "hicolor")
		for _, size := range []int{16, 22, 24, 32, 48, 64, 128} {
			dir := filepath.Join(base, fmt.Sprintf("%dx%d", size, size), "status")
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, iconName+".png"),
				icons.GenerateIcon(icons.ProviderLogos[providerName], pct, size), 0644)
		}
	}

	if iconName == currentIconName {
		// Same provider — force pixmap update (icon name unchanged so KDE won't re-read theme)
		pixmaps := make([][]byte, 0, 3)
		for _, size := range []int{16, 32, 64} {
			pixmaps = append(pixmaps, icons.GenerateIcon(icons.ProviderLogos[providerName], pct, size))
		}
		systray.SetIconNameWithPixmap(iconName, pixmaps)
		return
	}

	currentIconName = iconName
	pixmaps := make([][]byte, 0, 3)
	for _, size := range []int{16, 32, 64} {
		pixmaps = append(pixmaps, icons.GenerateIcon(icons.ProviderLogos[providerName], pct, size))
	}
	systray.SetIconNameWithPixmap(iconName, pixmaps)
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
