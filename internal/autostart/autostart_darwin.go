package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const supported = true

// macOS: LaunchAgent plist
//
// The path is interpolated into a <string> element, so it must be
// XML-escaped to keep the plist valid for paths containing &, <, or >.

// AbandonProcessGroup stops launchd from killing any child processes when
// the tray exits (e.g., a browser window the user opened via the Open
// Dashboard menu). ProcessType=Interactive gives the tray normal
// scheduling priority rather than Background's throttling.
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
    <key>AbandonProcessGroup</key>
    <true/>
    <key>ProcessType</key>
    <string>Interactive</string>
</dict>
</plist>
`

const launchAgentLabel = "com.clawmeter.tray"

func install(bin string) error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(renderLaunchAgentPlist(bin)), 0o644); err != nil {
		return err
	}
	// `launchctl load` was deprecated in macOS 10.11 (2015); the modern
	// API is bootstrap, with the gui/<uid> domain (only active during a
	// logged-in GUI session, which is exactly when a tray makes sense).
	// `bootout` first in case a stale entry exists from a previous install.
	target := guiDomain() + "/" + launchAgentLabel
	exec.Command("launchctl", "bootout", target).Run()
	return exec.Command("launchctl", "bootstrap", guiDomain(), path).Run()
}

func uninstall() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	exec.Command("launchctl", "bootout", guiDomain()+"/"+launchAgentLabel).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func guiDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func isInstalled() bool {
	path, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

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

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.clawmeter.tray.plist"), nil
}
