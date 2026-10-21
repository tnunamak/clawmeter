package browserext

import (
	"os"
	"path/filepath"
	"sync"
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

func TestSetupURLCarriesTheFolderInTheFragment(t *testing.T) {
	got := SetupURL("/home/a b/.local/share/clawmeter/extension")
	if got != SetupPage+"#dir=%2Fhome%2Fa+b%2F.local%2Fshare%2Fclawmeter%2Fextension" {
		t.Fatalf("SetupURL = %q", got)
	}
}

func TestInstallReplacesLinksWithoutWritingThroughThem(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(outside, "icons", "icon-128.png")
	if err := os.WriteFile(keep, []byte("not ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The install folder's icons folder links outside it.
	dir := filepath.Join(root, "clawmeter", "extension")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "icons"), filepath.Join(dir, "icons")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := installTo(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(keep); string(data) != "not ours" {
		t.Fatal("wrote through a link")
	}
	if info, err := os.Lstat(filepath.Join(dir, "icons")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("icons = %v, %v", info, err)
	}
	if !installed(dir) {
		t.Fatal("install incomplete")
	}

	// The install folder itself is a link: it becomes a real folder.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if err := installTo(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(keep); string(data) != "not ours" {
		t.Fatal("wrote through the linked folder")
	}
	if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 || !installed(dir) {
		t.Fatalf("folder = %v, %v", info, err)
	}
}

func TestConcurrentInstallsLeaveOneCompleteFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "extension")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- installTo(dir)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !installed(dir) {
		t.Fatal("install incomplete")
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	if len(entries) != 1 {
		t.Fatalf("staging left behind: %v", entries)
	}
}
