//go:build tray

package tray

import (
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/systray"
)

func TestMeterColorRolesKeepCanonicalText(t *testing.T) {
	for _, tc := range []struct {
		used, projection, pace float64
		red, green             int
	}{
		{70, 90, 50, 2, 0}, {20, 40, 60, 0, 4}, {50, 70, 50, 0, 0},
	} {
		cells := meter.Bar(tc.used, tc.projection, tc.pace, true)
		title := "📌 7d  " + meter.BarText(cells) + "  exact numbers"
		spans, key := meterSpans(title, cells)
		var plain strings.Builder
		red, green, bold := 0, 0, 0
		for _, span := range spans {
			plain.WriteString(span.Text)
			switch span.Style {
			case systray.TextRed:
				red++
			case systray.TextGreen:
				green++
			case systray.TextBold:
				bold++
			}
		}
		if plain.String() != title || key == "" || red != tc.red || green != tc.green || bold != 1 {
			t.Fatalf("style lost text or role: %q red%d green%d pace%d", plain.String(), red, green, bold)
		}
		if spans, _ := meterSpans(title+meter.BarText(cells), cells); spans != nil {
			t.Fatal("colored ambiguous provider text")
		}
	}
}
