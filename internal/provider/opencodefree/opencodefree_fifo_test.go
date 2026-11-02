//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package opencodefree

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFetchUsageCatalogReplacedWithFIFO(t *testing.T) {
	dir := t.TempDir()
	db, catalogPath := filepath.Join(dir, "opencode.db"), filepath.Join(dir, "models.json")
	if err := os.WriteFile(db, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte(`{"opencode":{"models":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Setup passed with a regular catalog. Replace it with a FIFO that has no
	// writer before collection starts.
	if err := os.Remove(catalogPath); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(catalogPath, 0600); err != nil {
		t.Fatal(err)
	}
	p := &Provider{dbPath: db, catalogPath: catalogPath}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.FetchUsage(ctx)
		done <- err
	}()
	// Cancel while the worker may already be in the catalog open. A fetch that
	// does not block on the FIFO must return an error either way.
	time.AfterFunc(100*time.Millisecond, cancel)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO catalog was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FetchUsage blocked opening a FIFO catalog with no writer")
	}
}
