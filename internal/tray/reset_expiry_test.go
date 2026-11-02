//go:build tray

package tray

import (
	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"strings"
	"testing"
	"time"
)

func TestResetExpiryMenuLifecycleAndDirectAction(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "claude", id: "work"}, false, nil, false)
	if len(menu.resetScheduleItems) != 1 || menu.resetScheduleStates[0].visible {
		t.Fatal("submenu was not established with a hidden placeholder")
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	r := &provider.UsageResetCredits{Snapshot: true, FetchedAt: now, Credits: []provider.UsageResetCredit{
		{Status: "available", ExpiresAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local)},
		{Status: "available", ExpiresAt: time.Date(2026, 11, 7, 17, 0, 0, 0, time.Local)},
	}}
	data := &provider.UsageData{ResetCredits: r}
	updateResetCreditRow(menu, data, now)
	if !menu.resetScheduleState.visible || !menu.resetState.enabled || !strings.Contains(menu.resetState.title, "Nov 7") {
		t.Fatalf("summary or disclosure missing: %+v", menu.resetState)
	}
	var titles []string
	for _, row := range menu.resetScheduleStates {
		if row.visible {
			titles = append(titles, row.title)
		}
	}
	text := strings.Join(titles, "\n")
	for _, want := range []string{menu.displayName, "Observed Nov 2, 2026", "Nov 6, 2026 at 09:00", "Nov 7, 2026 at 17:00", "resets may have been spent"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	var opened []string
	openResetRow(menu, func(url string) { opened = append(opened, url) })
	if len(opened) != 1 || opened[0] != claudeweb.UsageURL {
		t.Fatalf("direct action = %v", opened)
	}
	r.Credits = r.Credits[:1]
	updateResetCreditRow(menu, data, now)
	for _, row := range menu.resetScheduleStates {
		if row.visible && strings.Contains(row.title, "Nov 7") {
			t.Fatal("removed deadline survived refresh")
		}
	}
	updateResetCreditRow(menu, nil, now)
	if menu.resetScheduleState.visible || menu.resetState.visible {
		t.Fatal("empty inventory retained rows")
	}
	for _, row := range menu.resetScheduleStates {
		if row.visible {
			t.Fatal("hidden submenu retained visible children")
		}
	}
	openResetRow(menu, func(url string) { opened = append(opened, url) })
	if len(opened) != 1 {
		t.Fatal("hidden action opened browser")
	}
}
