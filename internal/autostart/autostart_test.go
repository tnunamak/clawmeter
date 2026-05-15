package autostart

import (
	"strings"
	"testing"
)

func TestEscapeDesktopExec(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/usr/bin/clawmeter", `"/usr/bin/clawmeter"`},
		{"space", "/home/joe smith/bin/clawmeter", `"/home/joe smith/bin/clawmeter"`},
		{"backslash", `C:\Program Files\clawmeter.exe`, `"C:\\Program Files\\clawmeter.exe"`},
		{"dollar", "/opt/$user/clawmeter", `"/opt/\$user/clawmeter"`},
		{"quote", `/tmp/"weird"/clawmeter`, `"/tmp/\"weird\"/clawmeter"`},
		{"backtick", "/tmp/`cmd`/clawmeter", "\"/tmp/\\`cmd\\`/clawmeter\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := escapeDesktopExec(c.in)
			if got != c.want {
				t.Fatalf("escapeDesktopExec(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRenderDesktopEntryContainsExec(t *testing.T) {
	out := renderDesktopEntry("/home/joe smith/bin/clawmeter")
	if !strings.Contains(out, `Exec="/home/joe smith/bin/clawmeter" tray`) {
		t.Fatalf("desktop entry missing escaped Exec line:\n%s", out)
	}
	if !strings.Contains(out, "Type=Application") {
		t.Fatalf("desktop entry missing Type line:\n%s", out)
	}
}

func TestEscapeXML(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/usr/bin/clawmeter", "/usr/bin/clawmeter"},
		{"/opt/a&b/clawmeter", "/opt/a&amp;b/clawmeter"},
		{"/opt/<a>/clawmeter", "/opt/&lt;a&gt;/clawmeter"},
		{`/opt/"a"/clawmeter`, "/opt/&quot;a&quot;/clawmeter"},
		{"/opt/'a'/clawmeter", "/opt/&apos;a&apos;/clawmeter"},
	}
	for _, c := range cases {
		if got := escapeXML(c.in); got != c.want {
			t.Fatalf("escapeXML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderLaunchAgentPlistEscapesPath(t *testing.T) {
	out := renderLaunchAgentPlist("/opt/a&b/clawmeter")
	if !strings.Contains(out, "<string>/opt/a&amp;b/clawmeter</string>") {
		t.Fatalf("plist did not XML-escape the binary path:\n%s", out)
	}
	if strings.Contains(out, "<string>/opt/a&b/clawmeter</string>") {
		t.Fatalf("plist contains raw ampersand (invalid XML):\n%s", out)
	}
}

func TestIsSupportedKnownPlatforms(t *testing.T) {
	// Sanity: function returns a stable bool; covered platforms are linux/darwin.
	// We don't override runtime.GOOS in tests — just exercise it.
	_ = IsSupported()
}
