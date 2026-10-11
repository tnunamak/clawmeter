package jetbrains

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
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

func TestJetBrainsConfigDir(t *testing.T) {
	tests := []struct {
		name string
		goos string
		home string
		env  map[string]string
		want string
	}{
		{
			name: "darwin",
			goos: "darwin",
			home: "/home/user",
			want: filepath.Join("/home/user", "Library", "Application Support", "JetBrains"),
		},
		{
			name: "windows with APPDATA",
			goos: "windows",
			home: "/home/user",
			env:  map[string]string{"APPDATA": "/users/user/AppData/Roaming"},
			want: filepath.Join("/users/user/AppData/Roaming", "JetBrains"),
		},
		{
			name: "windows without APPDATA",
			goos: "windows",
			home: "/home/user",
			want: filepath.Join("/home/user", ".config", "JetBrains"),
		},
		{
			name: "linux with XDG_CONFIG_HOME",
			goos: "linux",
			home: "/home/user",
			env:  map[string]string{"XDG_CONFIG_HOME": "/home/user/.local/config"},
			want: filepath.Join("/home/user/.local/config", "JetBrains"),
		},
		{
			name: "linux without XDG_CONFIG_HOME",
			goos: "linux",
			home: "/home/user",
			want: filepath.Join("/home/user", ".config", "JetBrains"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jetBrainsConfigDir(tt.goos, tt.home, tt.env); got != tt.want {
				t.Errorf("jetBrainsConfigDir() = %q, want %q", got, tt.want)
			}
		})
	}
}
