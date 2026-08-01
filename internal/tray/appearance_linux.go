//go:build tray && linux

package tray

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

// detectTrayPalette reads the freedesktop appearance portal, the only
// cross-toolkit desktop signal intended for this purpose. Unknown or missing
// settings preserve V10's dark default instead of guessing from theme names.
func detectTrayPalette() icons.TrayPalette {
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		return icons.TrayPaletteDark
	}
	defer bus.Close()

	var value dbus.Variant
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err = bus.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").
		CallWithContext(ctx, "org.freedesktop.portal.Settings.Read", 0, "org.freedesktop.appearance", "color-scheme").
		Store(&value)
	if err != nil {
		return icons.TrayPaletteDark
	}
	switch scheme := value.Value().(type) {
	case uint32:
		if scheme == 2 {
			return icons.TrayPaletteLight
		}
	case int32:
		if scheme == 2 {
			return icons.TrayPaletteLight
		}
	}
	return icons.TrayPaletteDark
}
