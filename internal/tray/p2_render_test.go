//go:build tray

package tray

import (
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
