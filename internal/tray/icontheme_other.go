//go:build tray && !linux

package tray

import (
	"fyne.io/systray"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func setupIconTheme()   {}
func cleanupIconTheme() {}

func setIconByName(_ string, data []byte) {
	systray.SetIcon(data)
}

func setIconDynamic(_ string, _ icons.MeterState, data []byte) {
	systray.SetIcon(data)
}
