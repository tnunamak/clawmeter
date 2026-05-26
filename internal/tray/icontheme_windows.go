//go:build tray && windows

package tray

import (
	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func setupIconTheme()   {}
func cleanupIconTheme() {}

// Windows tray displays one HICON at the system tray icon size. Passing in
// a 128x128 source means Windows downscales aggressively, which kills the
// label legibility. Re-render at the actual system tray size so the icon
// generator produces proportions tuned for that size.

func setIconByName(_ string, data []byte) {
	systray.SetIcon(data)
}

func setIconDynamic(providerName string, meter icons.MeterState, _ []byte) {
	size := systemTrayIconSize()
	icon := icons.GenerateProviderIconWithMeter(providerName, meter, size)
	systray.SetIcon(icon)
}

// systemTrayIconSize returns SM_CXSMICON: the size in pixels at which
// Windows wants notification-area icons drawn. This honors the active DPI
// scale (24 at 150%, 32 at 200%, etc.) so the icon renders crisply
// regardless of the user's display setup.
func systemTrayIconSize() int {
	const SM_CXSMICON = 49
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("GetSystemMetrics")
	size, _, _ := proc.Call(uintptr(SM_CXSMICON))
	if size < 16 {
		return 16
	}
	if size > 64 {
		return 64
	}
	return int(size)
}
