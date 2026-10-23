//go:build tray

package tray

import (
	"errors"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/systray"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestP2ProviderEnablementRace(t *testing.T) {
	menu := &providerMenuItems{provider: sourceMenuTestProvider{name: "openai"}}
	menus := map[string]*providerMenuItems{"openai": menu}
	config := config.DefaultConfig()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			applyProviderEnablement(menus, config)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			trayRenderMu.Lock()
			_ = menu.explicitlyEnabled
			trayRenderMu.Unlock()
		}
	}()
	wg.Wait()
}

func TestP2FailedProviderConnectionRestoresRetry(t *testing.T) {
	defer systray.ResetMenu()
	menu := &providerMenuItems{
		displayName: "Alibaba Token Plan",
		connectable: true,
		headerItem:  systray.AddMenuItem("Alibaba Token Plan · Connecting...", ""),
		headerState: menuItemState{title: "Alibaba Token Plan · Connecting...", visible: true, enabled: false, initialized: true},
	}
	menu.connecting.Store(true)

	finishProviderConnection(menu, errors.New("connection failed"))

	if menu.connecting.Load() {
		t.Fatal("connection remains marked as active")
	}
	if !menu.headerState.enabled || !menu.headerConnects.Load() || menu.headerState.title != "Alibaba Token Plan · "+retryConnectionAction {
		t.Fatalf("retry action not restored: %+v", menu.headerState)
	}
}

func TestP2PollTickerResetsOnlyWhenIntervalChanges(t *testing.T) {
	var resets []time.Duration
	reset := func(interval time.Duration) { resets = append(resets, interval) }

	current := resetPollIntervalIfChanged(300, 300, reset)
	if current != 300 || len(resets) != 0 {
		t.Fatalf("unchanged interval caused reset: current=%d resets=%v", current, resets)
	}
	current = resetPollIntervalIfChanged(current, 600, reset)
	if current != 600 || len(resets) != 1 || resets[0] != 10*time.Minute {
		t.Fatalf("changed interval was not applied once: current=%d resets=%v", current, resets)
	}
	current = resetPollIntervalIfChanged(current, 600, reset)
	if current != 600 || len(resets) != 1 {
		t.Fatalf("unchanged refresh postponed polling: current=%d resets=%v", current, resets)
	}
}

func TestP2OutageRemainsVisibleWithUsage(t *testing.T) {
	p := sourceMenuTestProvider{name: "openai"}
	menu := createProviderMenuItems(p, false, nil, false)
	data := &provider.UsageData{Provider: "openai", FetchedAt: time.Now(), Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 5, ResetsAt: time.Now().Add(4 * time.Hour)}}}
	s.mu.Lock()
	oldResults, oldStatus := s.lastResults, s.statuses
	s.lastResults = map[string]*provider.UsageData{"openai": data}
	s.statuses = map[string]*status.ProviderStatus{"openai": {Indicator: status.Major}}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.lastResults, s.statuses = oldResults, oldStatus
		s.mu.Unlock()
		systray.ResetMenu()
	}()
	updateUI(nil, nil, map[string]*providerMenuItems{"openai": menu}, systray.AddMenuItem("reauth", ""), newIconPicker(), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	if !menu.statusState.visible || !strings.Contains(strings.ToLower(menu.statusState.title), "outage") {
		t.Fatalf("outage hidden despite good usage: %+v", menu.statusState)
	}
	if !menu.windowStates[0].visible {
		t.Fatal("outage hid quota usage")
	}
}

func TestP2FailedProviderConnectionSurvivesRender(t *testing.T) {
	p := sourceMenuTestProvider{name: tokenPlanProviderName}
	menu := createProviderMenuItems(p, true, make(chan string), false)
	data := &provider.UsageData{Provider: tokenPlanProviderName, FetchedAt: time.Now(), IsExpired: true, Error: "token expired"}
	s.mu.Lock()
	oldResults, oldStatus := s.lastResults, s.statuses
	s.lastResults = map[string]*provider.UsageData{tokenPlanProviderName: data}
	s.statuses = nil
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.lastResults, s.statuses = oldResults, oldStatus
		s.mu.Unlock()
		systray.ResetMenu()
	}()
	render := func() {
		updateUI(nil, nil, map[string]*providerMenuItems{tokenPlanProviderName: menu}, systray.AddMenuItem("reauth", ""), newIconPicker(), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	}

	menu.connecting.Store(true)
	finishProviderConnection(menu, errors.New("connection failed"))
	render()
	if menu.headerAction != retryConnectionAction || !strings.HasSuffix(menu.headerState.title, " · "+retryConnectionAction) {
		t.Fatalf("retry action lost on the next render: %+v", menu.headerState)
	}

	menu.connecting.Store(true)
	finishProviderConnection(menu, nil)
	render()
	if menu.headerAction != "Reconnect quota access" || !menu.headerConnects.Load() {
		t.Fatalf("retry action kept after a successful connection: %+v", menu.headerState)
	}
}

func TestP2OutageRemainsVisibleWithStaleUsage(t *testing.T) {
	p := sourceMenuTestProvider{name: "openai"}
	menu := createProviderMenuItems(p, false, nil, false)
	data := &provider.UsageData{Provider: "openai", FetchedAt: time.Now(), Stale: true, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 5, ResetsAt: time.Now().Add(4 * time.Hour)}}}
	s.mu.Lock()
	oldResults, oldStatus := s.lastResults, s.statuses
	s.lastResults = map[string]*provider.UsageData{"openai": data}
	s.statuses = map[string]*status.ProviderStatus{"openai": {Indicator: status.Major}}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.lastResults, s.statuses = oldResults, oldStatus
		s.mu.Unlock()
		systray.ResetMenu()
	}()
	updateUI(nil, nil, map[string]*providerMenuItems{"openai": menu}, systray.AddMenuItem("reauth", ""), newIconPicker(), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	title := strings.ToLower(menu.statusState.title)
	if !menu.statusState.visible || !strings.Contains(title, "outage") || !strings.Contains(title, "last good data") {
		t.Fatalf("stale usage hid the outage: %+v", menu.statusState)
	}
}

func testIconPinPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clawmeter", "tray-icon-pin.json")
	old := iconPinPath
	iconPinPath = func() string { return path }
	t.Cleanup(func() { iconPinPath = old })
	return path
}

func TestIconPinPersistence(t *testing.T) {
	path := testIconPinPath(t)
	target := iconTarget{"claude:work", "7d All"}
	if got := loadIconPin(); got != (iconTarget{}) {
		t.Fatalf("missing file loaded %v", got)
	}
	if err := saveIconPin(target); err != nil {
		t.Fatal(err)
	}
	if got := loadIconPin(); got != target {
		t.Fatalf("loaded %v, want %v", got, target)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("pin permissions: %v, %v", info, err)
	}
	target.Window = "5h"
	if err := saveIconPin(target); err != nil || loadIconPin() != target {
		t.Fatalf("replacing pin: %v", err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary files left: %v, %v", files, err)
	}
	if err := saveIconPin(iconTarget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pin file not deleted: %v", err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadIconPin(); got != (iconTarget{}) {
		t.Fatalf("invalid file loaded %v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A directory cannot be read as a JSON file, even when tests run as root.
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if got := loadIconPin(); got != (iconTarget{}) {
		t.Fatalf("unreadable file loaded %v", got)
	}
}

func TestQuotaRowsPinTheIconPersistently(t *testing.T) {
	path := testIconPinPath(t)
	defer systray.ResetMenu()
	now := time.Now()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "openai"}, false, nil, false)
	menus := map[string]*providerMenuItems{"openai": menu}
	picker := newIconPicker()
	picker.forwardRows(menu)
	data := &provider.UsageData{Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{
		{Name: "5h", Length: 5 * time.Hour, Utilization: 75, ResetsAt: now.Add(4 * time.Hour)},
		{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 5, ResetsAt: now.Add(6 * 24 * time.Hour)},
		{Name: "no reset", Utilization: 10},
	}}
	s.mu.Lock()
	oldResults, oldStatuses, oldPin := s.lastResults, s.statuses, s.iconTargetOverride
	s.lastResults = map[string]*provider.UsageData{"openai": data}
	s.statuses, s.iconTargetOverride = nil, iconTarget{}
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.lastResults, s.statuses, s.iconTargetOverride = oldResults, oldStatuses, oldPin
		s.mu.Unlock()
	})
	reauth, empty, setup := systray.AddMenuItem("reauth", ""), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", "")
	render := func() { updateUI(nil, nil, menus, reauth, picker, empty, setup) }
	click := func(item *systray.MenuItem) {
		t.Helper()
		select {
		case item.ClickedCh <- struct{}{}:
		case <-time.After(time.Second):
			t.Fatal("click was not received")
		}
		select {
		case picked := <-picker.picked:
			selectIconTarget(menus, picker, picked)
		case <-time.After(time.Second):
			t.Fatal("click did not reach shared picker")
		}
	}
	pin := iconTarget{"openai", "7d"}
	assertChecks := func(want iconTarget) {
		t.Helper()
		for row, target := range menu.windowTargets {
			pinned := target.Provider != "" && target == want
			if strings.HasPrefix(menu.windowIcons[row], "pinned") != pinned {
				t.Fatalf("row %d pin mark %q disagrees with %v", row, menu.windowIcons[row], want)
			}
			if pinned && menu.windowIcons[row] != "pinned" && menu.windowIcons[row] != "pinned-risk" {
				t.Fatalf("row %d pin mark %q", row, menu.windowIcons[row])
			}
		}
	}
	render()
	if !menu.windowStates[1].enabled || menu.windowTargets[1] != pin {
		t.Fatalf("quota row not pinnable: %+v", menu.windowTargets)
	}
	if menu.windowStates[2].enabled || menu.windowTargets[2] != (iconTarget{}) {
		t.Fatal("window without a reset is pinnable")
	}
	for row, target := range menu.windowTargets {
		if target.Provider == "" && menu.windowStates[row].enabled {
			t.Fatalf("non-pinnable row %d enabled", row)
		}
	}
	autoMeter, autoTooltip := s.currentIconMeter, s.currentTooltip
	click(menu.windowItems[1])
	assertChecks(pin)
	if loadIconPin() != pin || s.currentIconMeter.Label == autoMeter.Label || s.currentTooltip == autoTooltip {
		t.Fatal("row pin did not persist or change icon and tooltip")
	}
	click(menu.windowItems[1])
	assertChecks(iconTarget{})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("row unpin did not delete pin: %v", err)
	}
	click(menu.windowItems[1])
	assertChecks(pin)
	s.mu.Lock()
	s.lastResults = map[string]*provider.UsageData{"openai": {Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{data.Windows[0], data.Windows[2]}}}
	s.mu.Unlock()
	render()
	assertChecks(iconTarget{})
	if loadIconPin() != pin || currentIconPin() == nil {
		t.Fatal("missing quota erased the saved pin")
	}
	if s.currentIconMeter.Label != autoMeter.Label || s.currentIconMeter.UsagePct != autoMeter.UsagePct {
		t.Fatalf("missing quota did not display Automatic: got %+v, initial %+v", s.currentIconMeter, autoMeter)
	}
	s.mu.Lock()
	s.lastResults = map[string]*provider.UsageData{"openai": data}
	s.mu.Unlock()
	render()
	assertChecks(pin)
	if s.currentIconMeter.Label == autoMeter.Label {
		t.Fatal("returned quota did not restore pinned icon")
	}
	// The pinned quota stays listed but loses its reset, so it cannot be
	// pinned: the icon follows Automatic, as the picker shows.
	noReset := data.Windows[1]
	noReset.ResetsAt = time.Time{}
	s.mu.Lock()
	s.lastResults = map[string]*provider.UsageData{"openai": {Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{data.Windows[0], noReset, data.Windows[2]}}}
	s.mu.Unlock()
	render()
	assertChecks(iconTarget{})
	if s.currentIconMeter.Label != autoMeter.Label || loadIconPin() != pin {
		t.Fatalf("pin without a reset still drives the icon: %+v", s.currentIconMeter)
	}
	s.mu.Lock()
	s.lastResults = map[string]*provider.UsageData{"openai": data}
	s.mu.Unlock()
	render()
	assertChecks(pin)
	click(menu.windowItems[1])
	assertChecks(iconTarget{})
	if loadIconPin() != (iconTarget{}) {
		t.Fatal("unpinning did not clear saved pin")
	}
}
