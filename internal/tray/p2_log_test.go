//go:build tray

package tray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestP2RuntimePanicCaptured(t *testing.T) {
	if os.Getenv("CLAWMETER_P2_LOG_CHILD") == "1" {
		redirectLogToFile()
		panic("synthetic-tray-crash")
	}
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestP2RuntimePanicCaptured$")
	cmd.Env = append(os.Environ(), "CLAWMETER_P2_LOG_CHILD=1", "HOME="+home, "USERPROFILE="+home, "XDG_CACHE_HOME="+home, "LOCALAPPDATA="+home, "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("panic fixture returned success: %s", out)
	}
	dir := home
	if _, err := os.Stat(filepath.Join(home, "Library", "Caches")); err == nil {
		dir = filepath.Join(home, "Library", "Caches")
	}
	log, err := os.ReadFile(filepath.Join(dir, "clawmeter", "tray.log"))
	if err != nil || !strings.Contains(string(log), "synthetic-tray-crash") {
		t.Fatalf("runtime panic missing from tray log: %s %v", log, err)
	}
}
