//go:build tray

package tray

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRotatingLogMovesOversizedLogAndReplacesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tray.log")
	backup := path + ".1"
	if err := os.WriteFile(path, []byte("old log contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("stale backup"), 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := openRotatingLog(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(backup); err != nil || string(got) != "old log contents" {
		t.Fatalf("backup = %q, %v; want previous log contents", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("new log stat = %v, %v; want empty log", info, err)
	}
}

func TestOpenRotatingLogKeepsLogAtSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.log")
	if err := os.WriteFile(path, []byte("12345678"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openRotatingLog(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("backup exists at size limit: %v", err)
	}
}
