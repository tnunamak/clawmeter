//go:build tray && !linux

package tray

import "fyne.io/systray"

func setupIconTheme()   {}
func cleanupIconTheme() {}

func setIconByName(_ string, data []byte) {
	systray.SetIcon(data)
}

func setIconDynamic(_ string, _ float64, data []byte) {
	systray.SetIcon(data)
}
