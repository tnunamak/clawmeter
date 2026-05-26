package autostart

import (
	"strings"
	"testing"
)

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
