//go:build tray && !linux

package tray

import "fyne.io/systray"

func setupIconTheme()   {}
func cleanupIconTheme() {}

func setIconByName(_ string, data []byte) {
	systray.SetIcon(data)
}

// setDynamicIcon sets the tray icon from dynamically rendered PNG bytes.
func setDynamicIcon(pngData []byte) {
	systray.SetIcon(pngData)
}
