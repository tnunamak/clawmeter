//go:build tray && windows

package tray

import (
	"sync"
	"unsafe"

	"github.com/tnunamak/clawmeter/internal/systray"
	"golang.org/x/sys/windows"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

var (
	trayPalette      = icons.TrayPaletteDark
	iconThemeMu      sync.Mutex
	lastIconProvider string
	lastIconMeter    icons.MeterState
	hasDynamicIcon   bool

	user32                 = windows.NewLazySystemDLL("user32.dll")
	findWindow             = user32.NewProc("FindWindowW")
	getDpiForWindow        = user32.NewProc("GetDpiForWindow")
	getDpiForSystem        = user32.NewProc("GetDpiForSystem")
	getSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	getSystemMetrics       = user32.NewProc("GetSystemMetrics")
)

func setupIconTheme() {
	iconThemeMu.Lock()
	trayPalette = resolveTrayPalette()
	iconThemeMu.Unlock()
	systray.SetAppearanceChangeHandler(refreshIconTheme)
}

func cleanupIconTheme() { systray.SetAppearanceChangeHandler(nil) }

// Windows tray displays one HICON at the system tray icon size. Passing in
// a 128x128 source means Windows downscales aggressively, which kills the
// label legibility. Re-render at the actual system tray size so the icon
// generator produces proportions tuned for that size.

func setIconByName(_ string, data []byte) {
	iconThemeMu.Lock()
	defer iconThemeMu.Unlock()
	hasDynamicIcon = false
	systray.SetIcon(data)
}

func setIconDynamic(providerName string, meter icons.MeterState, _ []byte) {
	iconThemeMu.Lock()
	defer iconThemeMu.Unlock()
	lastIconProvider, lastIconMeter, hasDynamicIcon = providerName, meter, true
	renderDynamicIcon()
}

func refreshIconTheme() {
	palette := resolveTrayPalette()
	iconThemeMu.Lock()
	defer iconThemeMu.Unlock()
	if palette == trayPalette {
		return
	}
	trayPalette = palette
	if hasDynamicIcon {
		renderDynamicIcon()
	}
}

// Caller holds iconThemeMu.
func renderDynamicIcon() {
	size := systemTrayIconSize()
	icon := icons.GenerateProviderIconWithMeterPalette(lastIconProvider, lastIconMeter, size, trayPalette)
	systray.SetIcon(icon)
}

// systemTrayIconSize uses the taskbar's DPI, which can differ from the
// process's system DPI when the taskbar is on another monitor.
func systemTrayIconSize() int {
	const SM_CXSMICON = 49
	var dpi uintptr
	if getDpiForWindow.Find() == nil {
		taskbarClass, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
		taskbar, _, _ := findWindow.Call(uintptr(unsafe.Pointer(taskbarClass)), 0)
		if taskbar != 0 {
			dpi, _, _ = getDpiForWindow.Call(taskbar)
		}
	}
	if dpi == 0 && getDpiForSystem.Find() == nil {
		dpi, _, _ = getDpiForSystem.Call()
	}
	var size uintptr
	if dpi != 0 && getSystemMetricsForDpi.Find() == nil {
		size, _, _ = getSystemMetricsForDpi.Call(uintptr(SM_CXSMICON), dpi)
	}
	if size == 0 {
		size, _, _ = getSystemMetrics.Call(uintptr(SM_CXSMICON))
	}
	if size < 16 {
		return 16
	}
	if size > 64 {
		return 64
	}
	return int(size)
}
