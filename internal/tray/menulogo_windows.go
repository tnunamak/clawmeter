//go:build tray && windows

package tray

import "github.com/tnunamak/clawmeter/internal/systray"

// Windows menu icons need an .ico file; menu rows stay text-only there.
func setMenuItemIcon(*systray.MenuItem, []byte) {}
