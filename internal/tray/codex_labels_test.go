//go:build tray

package tray

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

// Every window shape the Codex provider can emit must give the Claw Frame icon
// some window text. Scoped windows ("5h Spark", "7d Review") carry badge codes
// such as "5S" and "7R" that the renderer must still draw.
func TestCodexWindowLabelsYieldFrameLabel(t *testing.T) {
	scopes := []string{"", "Review", "Spark", "Spark 5.4", "Spark 2", "Extra", "5.4 Pro", "Codex-Max"}
	for _, scope := range scopes {
		for _, base := range []struct{ name, display string }{{"5h", "5h"}, {"7d", "7 days"}} {
			w := provider.UsageWindow{Name: base.name, DisplayName: base.display}
			if scope != "" {
				w.Name = base.name + " " + scope
				if base.name == "5h" {
					w.DisplayName = "5h (" + scope + ")"
				} else {
					w.DisplayName = "7 days (" + scope + ")"
				}
			}
			badge := windowBadgeLabelForWindow(w)
			if got := icons.FrameDisplayLabel(badge); got == "" {
				t.Errorf("window %q (display %q): badge %q renders no frame label", w.Name, w.DisplayName, badge)
			}
		}
	}
}
