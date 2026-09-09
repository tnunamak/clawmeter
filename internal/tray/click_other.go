//go:build tray && !linux && !freebsd && !openbsd && !netbsd

package tray

import "github.com/tnunamak/clawmeter/internal/systray"

func installTrayClickHandlers(iconClickCh chan<- iconClickAction) {
	dispatcher := newTrayClickDispatcher(iconClickCh, trayDoubleClickWindow)
	systray.SetOnTapped(dispatcher.tapped)
}
