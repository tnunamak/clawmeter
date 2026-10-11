package jetbrains

import (
	"github.com/tnunamak/clawmeter/internal/config"
	"path/filepath"
	"runtime"
	"testing"
)

func TestP2NativeXDGQuotaPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG fixture")
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", root)
	path := filepath.Join(root, "JetBrains", "Idea2026.1", "options", "AIAssistantQuotaManager2.xml")
	writeQuotaFile(t, path, 100, 25)
	got, err := New(config.ProviderConfig{}).findQuotaFile()
	if err != nil || got != path {
		t.Fatalf("native discovery: %q %v", got, err)
	}
}
