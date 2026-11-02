//go:build tray && !linux

package tray

import (
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func setupTooltipColors()                           {}
func stopTooltipColors()                            {}
func nativeTooltipImage(view.Snapshot) tooltipImage { return tooltipImage{} }
func setNativeTooltip(text string, _ tooltipImage)  { systray.SetTooltip(text) }
