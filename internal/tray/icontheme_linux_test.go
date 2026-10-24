//go:build tray

package tray

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func TestDynamicIconDataUsesNativeProviderRenderer(t *testing.T) {
	meter := icons.MeterState{UsagePct: 68, ExpectedPct: 35, ShowExpected: true, Label: "7D"}
	want := icons.GenerateProviderIconWithMeter("openai", meter, 32)
	got := dynamicIconData("openai", meter, []byte("deliberately ignored legacy 128px raster"), 32)
	if !bytes.Equal(got, want) {
		t.Fatal("32px Linux pixmap was not rendered from the native icon geometry")
	}
}

func TestDynamicIconSizesLeadWithNativeV10Rasters(t *testing.T) {
	want := []int{22, 32, 64, 128}
	if len(dynamicIconSizes) != len(want) {
		t.Fatalf("dynamic icon sizes = %v, want %v", dynamicIconSizes, want)
	}
	for i, size := range want {
		if dynamicIconSizes[i] != size {
			t.Fatalf("dynamic icon sizes = %v, want %v", dynamicIconSizes, want)
		}
	}
}

func TestSetupIconThemeRefreshesOnlyInstalledLauncherIcons(t *testing.T) {
	paths := []string{
		filepath.Join(".local", "share", "pixmaps", "clawmeter.png"),
		filepath.Join(".local", "share", "icons", "hicolor", "1024x1024", "apps", "clawmeter.png"),
	}
	// Exercise each destination independently, including an absent sibling.
	for _, installed := range paths {
		t.Run(installed, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			// Never invoke a real desktop cache updater from this unit test.
			t.Setenv("PATH", t.TempDir())
			oldPalette := trayPalette
			t.Cleanup(func() { trayPalette = oldPalette })
			path := filepath.Join(home, installed)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("old launcher logo"), 0640); err != nil {
				t.Fatal(err)
			}

			installedInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			setupIconTheme()
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, icons.App512) {
				t.Fatal("installed launcher icon was not refreshed with App512")
			}
			config, err := png.DecodeConfig(bytes.NewReader(got))
			if err != nil || config.Width != 512 || config.Height != 512 {
				t.Fatalf("launcher icon dimensions = %dx%d, err = %v; want 512x512 PNG", config.Width, config.Height, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != installedInfo.Mode().Perm() {
				t.Fatalf("launcher permissions = %o, want %o", info.Mode().Perm(), installedInfo.Mode().Perm())
			}
			for _, other := range paths {
				if other == installed {
					continue
				}
				if _, err := os.Stat(filepath.Join(home, other)); !os.IsNotExist(err) {
					t.Fatalf("missing launcher %s was created or inaccessible: %v", other, err)
				}
			}

			// Set a historical mtime so a rewrite is detectable even on file
			// systems with coarse timestamp precision, without sleeping.
			stamp := time.Unix(1000000000, 0)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			setupIconTheme()
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("matching launcher icon was rewritten")
			}
		})
	}
}

func TestRefreshInstalledLauncherIconsDoesNotCreateDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	refreshInstalledLauncherIcons(home)
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatalf("refresh created missing launcher directories: %v", err)
	}
}
