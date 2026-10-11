//go:build tray && windows

package tray

import (
	"time"

	"golang.org/x/sys/windows"
)

var pGetDoubleClickTime = windows.NewLazySystemDLL("User32.dll").NewProc("GetDoubleClickTime")

// trayDoubleClickWindow follows the user's Windows double-click speed.
func trayDoubleClickWindow() time.Duration {
	ms, _, _ := pGetDoubleClickTime.Call()
	if ms == 0 {
		return defaultDoubleClickWindow
	}
	return time.Duration(ms) * time.Millisecond
}
