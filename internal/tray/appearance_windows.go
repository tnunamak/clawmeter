//go:build tray && windows

package tray

import (
	"golang.org/x/sys/windows/registry"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func detectTrayPalette() icons.TrayPalette {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return icons.TrayPaletteDark
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	if err == nil && value != 0 {
		return icons.TrayPaletteLight
	}
	return icons.TrayPaletteDark
}
