//go:build tray

package tray

import (
	"errors"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/systray"
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
	updateUI(nil, nil, map[string]*providerMenuItems{"openai": menu}, systray.AddMenuItem("reauth", ""), systray.AddMenuItem("icon", ""), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
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
		updateUI(nil, nil, map[string]*providerMenuItems{tokenPlanProviderName: menu}, systray.AddMenuItem("reauth", ""), systray.AddMenuItem("icon", ""), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
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
	updateUI(nil, nil, map[string]*providerMenuItems{"openai": menu}, systray.AddMenuItem("reauth", ""), systray.AddMenuItem("icon", ""), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	title := strings.ToLower(menu.statusState.title)
	if !menu.statusState.visible || !strings.Contains(title, "outage") || !strings.Contains(title, "last good data") {
		t.Fatalf("stale usage hid the outage: %+v", menu.statusState)
	}
}
