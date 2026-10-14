//go:build tray && !windows

package tray

import (
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func setMenuItemLogo(item *systray.MenuItem, logo []byte) {
	item.SetIcon(icons.MenuLogo(logo, 32))
}
