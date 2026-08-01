//go:build tray && darwin

package tray

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func detectTrayPalette() icons.TrayPalette {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err == nil && strings.EqualFold(strings.TrimSpace(string(output)), "Dark") {
		return icons.TrayPaletteDark
	}
	return icons.TrayPaletteLight
}
