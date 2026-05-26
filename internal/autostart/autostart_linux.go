package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const supported = true

// Linux: XDG autostart .desktop file
//
// Per the Desktop Entry spec, the Exec= value is parsed with shell-like
// quoting: backslash, backtick, dollar sign and double-quote are reserved
// inside quoted strings. We always wrap the path in double quotes and
// escape the reserved characters so paths with spaces or symbols still work.

const desktopEntryTemplate = `[Desktop Entry]
Type=Application
Name=Clawmeter
Comment=AI usage monitor
Exec=%s tray
Terminal=false
X-GNOME-Autostart-enabled=true
`

func install(bin string) error {
	path, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(renderDesktopEntry(bin)), 0o644)
}

func uninstall() error {
	path, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func isInstalled() bool {
	path, err := desktopPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func renderDesktopEntry(bin string) string {
	return fmt.Sprintf(desktopEntryTemplate, escapeDesktopExec(bin))
}

func escapeDesktopExec(s string) string {
	// Reserved characters per the Desktop Entry "Quoting" section.
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	)
	return `"` + replacer.Replace(s) + `"`
}

func desktopPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "autostart", "clawmeter.desktop"), nil
}
