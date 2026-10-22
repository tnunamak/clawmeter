package main

import (
	"os"
	"strings"
	"testing"
)

func TestCodexResetAnnouncementsSetting(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	stdout, stderr, code := runWithHome(t, bin, home, "config", "show")
	if code != 0 || !strings.Contains(stdout, "Codex reset announcements: false") {
		t.Fatalf("default show: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	if stdout, stderr, code := runWithHome(t, bin, home, "config", "set", "codex_reset_announcements", "true"); code != 0 {
		t.Fatalf("set true: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runWithHome(t, bin, home, "config", "show")
	if code != 0 || !strings.Contains(stdout, "Codex reset announcements: true") {
		t.Fatalf("show after set: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	raw, err := os.ReadFile(configPathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "codex_reset_announcements: true") {
		t.Fatalf("config file lacks the setting:\n%s", raw)
	}

	if _, _, code := runWithHome(t, bin, home, "config", "set", "codex_reset_announcements", "maybe"); code == 0 {
		t.Fatal("non-boolean value accepted")
	}
}
