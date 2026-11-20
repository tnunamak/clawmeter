package all

import "testing"

// The bare selector retains its historical free-model meaning; the explicit
// account selector resolves to the account provider’s stable registry key.
func TestOpenCodeNames(t *testing.T) {
	for name, want := range map[string]string{
		"opencode":         "opencode_free",
		"opencode-free":    "opencode_free",
		"opencode_free":    "opencode_free",
		"opencode-account": "opencode",
	} {
		if got, ok := CanonicalName(name); !ok || got != want {
			t.Errorf("CanonicalName(%q) = %q, %t; want %q", name, got, ok, want)
		}
	}
}
