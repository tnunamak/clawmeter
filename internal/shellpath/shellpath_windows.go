package shellpath

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// capture reads the authoritative user + system PATH from the registry.
// This is what a freshly-spawned cmd.exe would see; what we inherit from
// Explorer is whatever PATH existed at login, which is often stale.
//
// We merge the two scopes in the same order Windows would (system first,
// then user) so the resulting list is identical to what `[Environment]::
// GetEnvironmentVariable('Path','Process')` returns in a new shell.
func capture() []string {
	var parts []string
	parts = append(parts, readRegistryPath(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)...)
	parts = append(parts, readRegistryPath(registry.CURRENT_USER, `Environment`)...)
	return parts
}

func readRegistryPath(root registry.Key, path string) []string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()

	// Path values may be REG_EXPAND_SZ with %SystemRoot% etc. — use the
	// expand variant so callers get a usable filesystem path.
	value, _, err := k.GetStringValue("Path")
	if err != nil {
		return nil
	}
	expanded := os.ExpandEnv(value)
	parts := strings.Split(expanded, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
