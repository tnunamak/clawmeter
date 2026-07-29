package shellpath

import (
	"os"
	"path/filepath"
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

func terminalAvailable() bool {
	return false
}

func readCurrentUserEnv(name string) (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	value, _, err := k.GetStringValue(name)
	return value, err == nil
}

func readRegistryPath(root registry.Key, path string) []string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()

	// Path values may be REG_EXPAND_SZ with %SystemRoot% etc. — use the
	// expand variant so callers get a usable filesystem path.
	value, valueType, err := k.GetStringValue("Path")
	if err != nil {
		return nil
	}
	return expandAndSplitRegistryPath(value, valueType)
}

func expandAndSplitRegistryPath(value string, valueType uint32) []string {
	if valueType == registry.EXPAND_SZ {
		expanded, err := registry.ExpandString(value)
		if err != nil {
			return nil
		}
		value = expanded
	}

	parts := strings.Split(value, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func pathEntryKey(path string) string {
	if path == "" {
		return ""
	}

	volume := filepath.VolumeName(path)
	rest := path[len(volume):]
	trimmed := strings.TrimRight(rest, `\\/`)
	switch {
	case trimmed != "":
		path = volume + trimmed
	case rest != "":
		// Preserve a root separator instead of collapsing C:\ to C:.
		path = volume + `\`
	case strings.HasPrefix(volume, `\\`):
		// A UNC share names its root even without the optional final slash.
		path = volume + `\`
	default:
		path = volume
	}
	return strings.ToLower(path)
}
