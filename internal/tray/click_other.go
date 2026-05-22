//go:build tray && !linux && !freebsd && !openbsd && !netbsd

package tray

import "fyne.io/systray"

func installTrayClickHandlers(iconClickCh chan<- iconClickAction) {
	dispatcher := newTrayClickDispatcher(iconClickCh, trayDoubleClickWindow)
	systray.SetOnTapped(dispatcher.tapped)
}
