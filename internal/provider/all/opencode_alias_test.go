package all

import "testing"

// The account provider is "opencode" and covers every plan the account has.
// The free-model collector keeps its own name, so its local observations are
// never reported as the account's usage.
func TestOpenCodeNames(t *testing.T) {
	for name, want := range map[string]string{
		"opencode":      "opencode",
		"opencode-free": "opencode_free",
	} {
		if got, ok := CanonicalName(name); !ok || got != want {
			t.Errorf("CanonicalName(%q) = %q, %t; want %q", name, got, ok, want)
		}
	}
}
