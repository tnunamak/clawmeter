package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

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

const desktopEntry = `[Desktop Entry]
Type=Application
Name=Clawmeter
Comment=Claude usage monitor
Exec=%s tray
Terminal=false
X-GNOME-Autostart-enabled=true
`

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
	return os.WriteFile(path, []byte(fmt.Sprintf(desktopEntry, bin)), 0644)
}

func uninstallLinux() error {
	path, err := linuxDesktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
}

// macOS: LaunchAgent plist

const launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
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
	if err := os.WriteFile(path, []byte(fmt.Sprintf(launchAgentPlist, bin)), 0644); err != nil {
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
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
}
