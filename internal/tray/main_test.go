//go:build tray

package tray

import (
	"os"
	"path/filepath"
	"testing"
)

// scopeUserDirs points the config and cache directories at home on every OS.
func scopeUserDirs(home string, set func(key, value string) error) {
	for key, value := range map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
	} {
		set(key, value)
	}
}

// TestMain keeps every test away from the user's saved tray icon pin, config
// and cache. A test
// that needs the file sets its own path with testIconPinPath.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "clawmeter-tray-test-")
	if err != nil {
		panic(err)
	}
	iconPinPath = func() string { return filepath.Join(dir, "tray-icon-pin.json") }
	// Config and cache paths come from these variables, so no test reads or
	// writes the user's own files.
	scopeUserDirs(filepath.Join(dir, "home"), os.Setenv)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
