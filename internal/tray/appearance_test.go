//go:build tray

package tray

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func TestConfiguredTrayPalette(t *testing.T) {
	for _, test := range []struct {
		value string
		want  icons.TrayPalette
		ok    bool
	}{
		{"dark", icons.TrayPaletteDark, true},
		{" LIGHT ", icons.TrayPaletteLight, true},
		{"system", "", false},
		{"", "", false},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("CLAWMETER_TRAY_PALETTE", test.value)
			got, ok := configuredTrayPalette()
			if got != test.want || ok != test.ok {
				t.Fatalf("configuredTrayPalette() = (%q, %t), want (%q, %t)", got, ok, test.want, test.ok)
			}
		})
	}
}
