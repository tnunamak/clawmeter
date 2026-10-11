package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestP2BackupCollisionPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"theme":"original"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := backupFile(path, "before-clawmeter-statusline")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(path, []byte(`{"theme":"changed"}`), 0600); err != nil {
			t.Fatal(err)
		}
		next, err := backupFile(path, "before-clawmeter-statusline")
		if err == nil && next == backup {
			t.Fatal("recovery snapshot reused with overwrite")
		}
	}
	got, err := os.ReadFile(backup)
	if err != nil || string(got) != string(original) {
		t.Fatalf("original backup overwritten: %s %v", got, err)
	}
}
