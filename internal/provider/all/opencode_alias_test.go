package all

import "testing"

// A bare "opencode" names the paid OpenCode Go plan. The free catalog keeps its
// own name, so the two cannot be confused.
func TestBareOpenCodeNamesTheGoPlan(t *testing.T) {
	for alias, want := range map[string]string{
		"opencode":      "opencode_go",
		"opencode-go":   "opencode_go",
		"opencode-free": "opencode_free",
	} {
		if got, ok := CanonicalName(alias); !ok || got != want {
			t.Errorf("CanonicalName(%q) = %q, %t; want %q", alias, got, ok, want)
		}
	}
}
