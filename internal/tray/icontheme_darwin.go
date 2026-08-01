//go:build tray && darwin

package tray

import (
	"fyne.io/systray"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

var trayPalette = icons.TrayPaletteDark

func setupIconTheme()   { trayPalette = resolveTrayPalette() }
func cleanupIconTheme() {}

func setIconByName(_ string, data []byte) {
	systray.SetIcon(data)
}

func setIconDynamic(providerName string, meter icons.MeterState, _ []byte) {
	// AppKit receives a single image from systray. Give it the hand-tuned
	// 32px V10 raster, rather than a 128px composite that would first have
	// been resized by our own renderer and then scaled again by macOS.
	systray.SetIcon(icons.GenerateProviderIconWithMeterPalette(providerName, meter, 32, trayPalette))
}
