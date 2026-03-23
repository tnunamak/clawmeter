package shellpath

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestMergeDeduplicates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not applicable on Windows")
	}

	orig := os.Getenv("PATH")
	defer os.Setenv("PATH", orig)

	os.Setenv("PATH", "/usr/bin:/usr/local/bin")
	merge([]string{"/usr/bin", "/home/user/.nvm/bin", "/usr/local/bin", "/opt/new"})

	got := os.Getenv("PATH")
	parts := strings.Split(got, ":")

	// Original entries should come first, in order
	if parts[0] != "/usr/bin" || parts[1] != "/usr/local/bin" {
		t.Errorf("original entries reordered: %v", parts)
	}

	// New entries appended
	if !strings.Contains(got, "/home/user/.nvm/bin") {
		t.Error("missing /home/user/.nvm/bin")
	}
	if !strings.Contains(got, "/opt/new") {
		t.Error("missing /opt/new")
	}

	// No duplicates
	seen := make(map[string]int)
	for _, p := range parts {
		seen[p]++
		if seen[p] > 1 {
			t.Errorf("duplicate entry: %s", p)
		}
	}
}

func TestInitNoopOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only applicable on Windows")
	}
	// Should not panic
	Init()
}
