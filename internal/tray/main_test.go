//go:build tray

package tray

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps every test away from the user's saved tray icon pin. A test
// that needs the file sets its own path with testIconPinPath.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "clawmeter-tray-test-")
	if err != nil {
		panic(err)
	}
	iconPinPath = func() string { return filepath.Join(dir, "tray-icon-pin.json") }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
