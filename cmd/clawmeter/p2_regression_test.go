package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestP2StatusShortcutEquals(t *testing.T) {
	for _, arg := range []string{"--provider=claude", "--json=true", "-source=work"} {
		if !isStatusShortcutFlag(arg) {
			t.Errorf("shortcut rejected %q", arg)
		}
	}
}
func TestP2SetupAndDoctorFailureExit(t *testing.T) {
	bin := buildBinary(t)
	for _, command := range []string{"setup", "doctor"} {
		t.Run(command, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{command}
			if command == "setup" {
				args = append(args, "--claude-statusline")
			}
			out, errout, code := runWithHome(t, bin, home, args...)
			if code == 0 {
				t.Fatalf("failure returned success: stdout=%q stderr=%q", out, errout)
			}
		})
	}
}
func TestP2DoctorConfigFailureExit(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	path := configPathForHome(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, errout, code := runWithHome(t, bin, home, "doctor"); code == 0 {
		t.Fatalf("config error returned success: %q %q", out, errout)
	}
}
