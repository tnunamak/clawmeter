//go:build tray && !windows

package tray

import "github.com/tnunamak/clawmeter/internal/systray"

func setMenuItemIcon(item *systray.MenuItem, icon []byte) {
	item.SetIcon(icon)
}
