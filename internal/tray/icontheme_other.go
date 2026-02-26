//go:build tray && !linux

package tray

import "fyne.io/systray"

func setupIconTheme()   {}
func cleanupIconTheme() {}

func setIconByName(_ string, data []byte) {
	systray.SetIcon(data)
}
