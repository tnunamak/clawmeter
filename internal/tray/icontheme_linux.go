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
	xdraw "golang.org/x/image/draw"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

var currentIconName string

const dynamicIconVersion = 33

// The Claw Frame artwork is designed natively at these two tray sizes. Keep
// the 22px form first: some SNI implementations retain the first pixmap as
// their initial/fallback icon, and 22px is the smallest approved raster.
var dynamicIconSizes = []int{22, 32, 64, 128}

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
	pixmaps := make([][]byte, 0, 4)
	for _, size := range []int{16, 32, 64, 128} {
		pixmaps = append(pixmaps, resizePNG(data, size))
	}
	systray.SetIconNameWithPixmap(iconName, pixmaps)
}

func setIconDynamic(providerName string, meter icons.MeterState, data128 []byte) {
	// Keep the visible tray identity stable by relying on IconPixmap instead of
	// a themed IconName. KDE can cache a stable themed name and ignore/delay
	// pixel updates, which makes the toast and icon disagree after cycling.
	iconName := dynamicIconName()
	currentIconName = iconName

	pixmaps := make([][]byte, 0, len(dynamicIconSizes))
	for _, size := range dynamicIconSizes {
		pixmaps = append(pixmaps, dynamicIconData(providerName, meter, data128, size))
	}
	systray.SetIconNameWithPixmap("", pixmaps)
}

func dynamicIconName() string {
	return fmt.Sprintf("clawmeter-dyn-v%d", dynamicIconVersion)
}

func dynamicIconData(providerName string, meter icons.MeterState, _ []byte, size int) []byte {
	// The V10 icon has independently drawn 22px and 32px forms. Do not
	// downsample a 128px composite: that destroys the native microtype and
	// half-pixel meter geometry on the common Linux tray sizes.
	return icons.GenerateProviderIconWithMeter(providerName, meter, size)
}

func resizePNG(data []byte, size int) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	srcBounds := src.Bounds()
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, srcBounds, xdraw.Over, nil)

	var buf bytes.Buffer
	png.Encode(&buf, dst)
	return buf.Bytes()
}
