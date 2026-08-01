//go:build tray

package tray

import (
	"os"
	"strings"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func configuredTrayPalette() (icons.TrayPalette, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CLAWMETER_TRAY_PALETTE"))) {
	case "dark":
		return icons.TrayPaletteDark, true
	case "light":
		return icons.TrayPaletteLight, true
	default:
		return "", false
	}
}

func resolveTrayPalette() icons.TrayPalette {
	if palette, ok := configuredTrayPalette(); ok {
		return palette
	}
	return detectTrayPalette()
}
