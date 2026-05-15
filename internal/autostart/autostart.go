package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// IsSupported reports whether autostart is implemented for the current OS.
// Tray UI uses this to decide whether to expose the toggle at all.
func IsSupported() bool {
	switch runtime.GOOS {
	case "linux", "darwin":
		return true
	default:
		return false
	}
}

func Install() error {
	bin, err := execPath()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "linux":
		return installLinux(bin)
	case "darwin":
		return installDarwin(bin)
	default:
		return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
	}
}

func IsInstalled() bool {
	switch runtime.GOOS {
	case "linux":
		path, err := linuxDesktopPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	case "darwin":
		path, err := darwinPlistPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	default:
		return false
	}
}

func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		return uninstallLinux()
	case "darwin":
		return uninstallDarwin()
	default:
		return fmt.Errorf("autostart not supported on %s", runtime.GOOS)
	}
}

func execPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

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

func linuxDesktopPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "autostart", "clawmeter.desktop"), nil
}

func installLinux(bin string) error {
	path, err := linuxDesktopPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(renderDesktopEntry(bin)), 0644)
}

func uninstallLinux() error {
	path, err := linuxDesktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// macOS: LaunchAgent plist
//
// The path is interpolated into a <string> element, so it must be
// XML-escaped to keep the plist valid for paths containing &, <, or >.

const launchAgentPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.clawmeter.tray</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>tray</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
</dict>
</plist>
`

func renderLaunchAgentPlist(bin string) string {
	return fmt.Sprintf(launchAgentPlistTemplate, escapeXML(bin))
}

func escapeXML(s string) string {
	replacer := strings.NewReplacer(
		`&`, `&amp;`,
		`<`, `&lt;`,
		`>`, `&gt;`,
		`"`, `&quot;`,
		`'`, `&apos;`,
	)
	return replacer.Replace(s)
}

func darwinPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.clawmeter.tray.plist"), nil
}

func installDarwin(bin string) error {
	path, err := darwinPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(renderLaunchAgentPlist(bin)), 0644); err != nil {
		return err
	}
	return exec.Command("launchctl", "load", path).Run()
}

func uninstallDarwin() error {
	path, err := darwinPlistPath()
	if err != nil {
		return err
	}
	exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
