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

const dynamicIconVersion = 31

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

func setIconDynamic(providerName string, meter icons.MeterState, data128 []byte) {
	// KDE can keep resolving a stale themed icon for stable dynamic names. Use
	// a versioned, bucketed name so visual rendering changes and severity
	// changes force a fresh tray icon lookup.
	iconName := dynamicIconName(providerName, meter)

	// Write to icon theme at multiple sizes
	home, err := os.UserHomeDir()
	if err == nil {
		base := filepath.Join(home, ".local", "share", "icons", "hicolor")
		for _, size := range []int{16, 22, 24, 32, 48, 64, 128} {
			dir := filepath.Join(base, fmt.Sprintf("%dx%d", size, size), "status")
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, iconName+".png"),
				dynamicIconData(providerName, meter, data128, size), 0644)
		}
	}

	if iconName == currentIconName {
		// Same provider — force pixmap update (icon name unchanged so KDE won't re-read theme)
		pixmaps := make([][]byte, 0, 3)
		for _, size := range []int{16, 32, 64} {
			pixmaps = append(pixmaps, dynamicIconData(providerName, meter, data128, size))
		}
		systray.SetIconNameWithPixmap(iconName, pixmaps)
		return
	}

	currentIconName = iconName
	pixmaps := make([][]byte, 0, 3)
	for _, size := range []int{16, 32, 64} {
		pixmaps = append(pixmaps, dynamicIconData(providerName, meter, data128, size))
	}
	systray.SetIconNameWithPixmap(iconName, pixmaps)
}

func dynamicIconName(providerName string, meter icons.MeterState) string {
	if providerName == "" {
		providerName = "none"
	}
	usageBucket := iconPctBucket(meter.UsagePct)
	expectedBucket := iconPctBucket(meter.ExpectedPct)
	riskBucket := iconPctBucket(meter.RiskPct)
	return fmt.Sprintf("clawmeter-dyn-v%d-%s-u%03d-e%03d-r%03d", dynamicIconVersion, providerName, usageBucket, expectedBucket, riskBucket)
}

func iconPctBucket(pct float64) int {
	bucket := int(pct + 0.5)
	if bucket < 0 {
		bucket = 0
	}
	if bucket > 100 {
		bucket = 100
	}
	return bucket
}

func dynamicIconData(providerName string, meter icons.MeterState, data128 []byte, size int) []byte {
	if size == 128 && len(data128) > 0 {
		return data128
	}
	if len(data128) > 0 {
		return resizePNG(data128, size)
	}
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
