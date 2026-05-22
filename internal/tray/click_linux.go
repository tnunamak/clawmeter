//go:build tray && (linux || freebsd || openbsd || netbsd) && !android

package tray

import "fyne.io/systray"

func installTrayClickHandlers(iconClickCh chan<- iconClickAction) {
	dispatcher := newTrayClickDispatcher(iconClickCh, trayDoubleClickWindow)
	systray.SetOnTapped(dispatcher.tapped)

	// With a left-click handler installed, the Linux StatusNotifier item is no
	// longer marked as menu-only. Return success for ContextMenu/secondary
	// activation so hosts can continue down the DBusMenu path instead of seeing
	// org.freedesktop.DBus.Error.UnknownMethod.
	systray.SetOnSecondaryTapped(func() {})
}
