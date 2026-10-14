//go:build tray && windows

package tray

import "github.com/tnunamak/clawmeter/internal/systray"

// Windows menu icons need an .ico file; provider headers stay text-only there.
func setMenuItemLogo(*systray.MenuItem, []byte) {}
