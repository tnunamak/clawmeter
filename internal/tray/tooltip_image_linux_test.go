//go:build tray && linux

package tray

import (
	"github.com/godbus/dbus/v5"
	"github.com/tnunamak/clawmeter/internal/view"
	"testing"
	"time"
)

func TestTooltipColorCapabilitiesAndThemeChange(t *testing.T) {
	for _, tc := range []struct {
		desktop string
		want    bool
	}{{"KDE", true}, {"Plasma", true}, {"KDE:Plasma", true}, {"notkde", false}, {"GNOME", false}, {"KDE:GNOME", false}, {"XFCE", false}} {
		if got := supportsTooltipImages(tc.desktop); got != tc.want {
			t.Fatalf("%s capability=%v", tc.desktop, got)
		}
	}
	for _, tc := range []struct {
		value any
		want  uint32
	}{{dbus.MakeVariant(dbus.MakeVariant(uint32(1))), 1}, {uint32(2), 2}, {int32(2), 2}, {uint32(0), 0}, {"dark", 0}} {
		if got := tooltipScheme(tc.value); got != tc.want {
			t.Fatalf("scheme %v => %d", tc.value, got)
		}
	}
	oldScheme := tooltipColorScheme.Load()
	defer tooltipColorScheme.Store(oldScheme)
	oldText, oldImage := s.currentTooltip, s.currentTooltipImage
	defer func() { s.currentTooltip, s.currentTooltipImage = oldText, oldImage }()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	snap := view.Snapshot{At: now, Selected: &view.Target{AccountKey: "work", Window: "5h"}, Accounts: []view.Account{{Key: "work", Label: "Work", Reading: view.Reading{State: view.ReadingFresh, ObservedAt: now}, Windows: []view.Window{{Name: "5h", Used: 70, Pace: 50, HasPace: true, Forecast: view.Forecast{State: view.ForecastKnown, Pct: 90}, Reset: view.Reset{Known: true, In: time.Hour}}}}}}
	tooltipColorScheme.Store(1)
	updateTrayTooltip(snap, nil)
	dark, text := s.currentTooltipImage, s.currentTooltip
	if dark.SVG == "" || dark.Alt != "█████┃██▒▒░" {
		t.Fatalf("missing canonical image: %+v", dark)
	}
	tooltipColorScheme.Store(2)
	updateTrayTooltip(snap, nil)
	if s.currentTooltipImage == dark || s.currentTooltipImage.SVG == "" || s.currentTooltip != text {
		t.Fatal("theme-only update failed or changed numeric text")
	}
	tooltipColorScheme.Store(0)
	updateTrayTooltip(snap, nil)
	if s.currentTooltipImage.SVG != "" || s.currentTooltip != text {
		t.Fatal("unknown theme did not restore complete plain text")
	}
}
