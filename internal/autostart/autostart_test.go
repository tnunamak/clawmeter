package autostart

import "testing"

func TestIsSupportedOnSupportedPlatforms(t *testing.T) {
	// All three first-class platforms (linux, darwin, windows) must report
	// supported=true. Other platforms (freebsd, openbsd, etc.) fall through
	// to the build-tagged "other" file where supported=false.
	if !IsSupported() {
		t.Fatalf("autostart should be supported on this platform")
	}
}
