//go:build windows

package shellpath

import (
	"os"
	"reflect"
	"testing"

	"github.com/tnunamak/clawmeter/internal/provider"
	"golang.org/x/sys/windows/registry"
)

func TestExpandAndSplitRegistryPath(t *testing.T) {
	t.Setenv("CLAWMETER_TEST_ROOT", `C:\Tools`)

	tests := []struct {
		name  string
		value string
		type_ uint32
		want  []string
	}{
		{name: "expand string", value: `%CLAWMETER_TEST_ROOT%;;C:\Bin`, type_: registry.EXPAND_SZ, want: []string{`C:\Tools`, `C:\Bin`}},
		{name: "preserve string literally", value: `%CLAWMETER_TEST_ROOT%;C:\Bin`, type_: registry.SZ, want: []string{`%CLAWMETER_TEST_ROOT%`, `C:\Bin`}},
		{name: "drop empty entries", value: `;C:\Bin;;`, type_: registry.SZ, want: []string{`C:\Bin`}},
		{name: "expansion failure", value: "C:\\Tools\x00;C:\\Bin", type_: registry.EXPAND_SZ},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandAndSplitRegistryPath(tt.value, tt.type_); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expandAndSplitRegistryPath() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPathEntryKeyWindowsNormalization(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "case", path: `C:\Tools`, want: `c:\tools`},
		{name: "trailing slash", path: `C:\Tools\`, want: `c:\tools`},
		{name: "drive relative", path: `C:`, want: `c:`},
		{name: "volume root", path: `C:\`, want: `c:\`},
		{name: "relative root", path: `\`, want: `\`},
		{name: "unc root without slash", path: `\\server\share`, want: `\\server\share\`},
		{name: "unc root", path: `\\server\share\`, want: `\\server\share\`},
		{name: "unc child", path: `\\server\share\Tools\`, want: `\\server\share\tools`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathEntryKey(tt.path); got != tt.want {
				t.Fatalf("pathEntryKey(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestMergeWindowsDeduplicatesCaseAndTrailingSeparators(t *testing.T) {
	orig := os.Getenv("PATH")
	defer os.Setenv("PATH", orig)

	os.Setenv("PATH", `C:\Tools;C:\Windows`)
	merge([]string{`c:\tools\`, `C:\New`})

	want := `C:\Tools;C:\Windows;C:\New`
	if got := os.Getenv("PATH"); got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
}

func TestSessionEnvironmentResolverDoesNotUseShellOnWindows(t *testing.T) {
	t.Setenv("CLAWMETER_WINDOWS_CREDENTIAL", "")
	got := NewSessionEnvironmentResolver().ResolveSessionEnvironment(provider.SessionEnvironmentRequest{EnvNames: []string{"CLAWMETER_WINDOWS_CREDENTIAL"}, AllowSessionEnvironmentFallback: true})
	if len(got) != 0 {
		t.Fatalf("recovered environment = %#v, want empty on Windows", got)
	}
	if got := os.Getenv("CLAWMETER_WINDOWS_CREDENTIAL"); got != "" {
		t.Fatalf("credential environment changed to %q", got)
	}
}

func TestSessionEnvironmentResolverUsesInheritedBeforeExactRegistryNames(t *testing.T) {
	t.Setenv("CLAWMETER_INHERITED", "process-value")
	t.Setenv("CLAWMETER_PERSISTED", "")
	var lookedUp []string
	resolver := newSessionEnvironmentResolverWithRegistryLookup(func(name string) (string, bool) {
		lookedUp = append(lookedUp, name)
		if name == "CLAWMETER_PERSISTED" {
			return "persistent-value", true
		}
		return "", false
	})
	request := provider.SessionEnvironmentRequest{EnvNames: []string{"CLAWMETER_MISSING", "CLAWMETER_PERSISTED", "CLAWMETER_INHERITED"}, AllowSessionEnvironmentFallback: true}
	got := resolver.ResolveSessionEnvironment(request)
	if got["CLAWMETER_INHERITED"] != "process-value" || got["CLAWMETER_PERSISTED"] != "persistent-value" {
		t.Fatalf("resolved values = %#v, want inherited and persisted values", got)
	}
	if !reflect.DeepEqual(lookedUp, []string{"CLAWMETER_MISSING", "CLAWMETER_PERSISTED"}) {
		t.Fatalf("registry names = %#v, want exact inherited misses", lookedUp)
	}
	resolver.ResolveSessionEnvironment(request)
	if len(lookedUp) != 2 {
		t.Fatalf("registry lookup count = %d, want cached second lookup", len(lookedUp))
	}
}
