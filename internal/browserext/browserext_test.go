package browserext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesTheExtensionAndRemovesStaleFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "extension")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "old.js")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installTo(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "background.js", "popup.html", filepath.Join("icons", "icon-128.png")} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file kept: %v", err)
	}
	// A second install leaves the files as they are.
	before, _ := os.Stat(filepath.Join(dir, "manifest.json"))
	if err := installTo(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(dir, "manifest.json"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("an unchanged file was rewritten")
	}
}

func TestGuideNamesTheFolderSafely(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a<b>", "extension")
	page, err := WriteGuide(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if !strings.Contains(html, "Load unpacked") || !strings.Contains(html, "a&lt;b&gt;") || strings.Contains(html, "a<b>") {
		t.Fatalf("guide = %s", html)
	}
}
