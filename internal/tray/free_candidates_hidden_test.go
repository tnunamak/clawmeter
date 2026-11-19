//go:build tray

package tray

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
)

// A catalog candidate with no recorded use is not activity. A paid account
// must not see a free row for it.
func TestOpenCodeFreeCandidatesWithoutUseStayHidden(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "opencode_free"}, false, nil, false)
	now := time.Now()
	s.mu.Lock()
	oldResults := s.lastResults
	s.lastResults = map[string]*provider.UsageData{"opencode_free": {
		Provider: "opencode_free", FetchedAt: now,
		LocalModels: []provider.LocalModelUsage{{Model: "example-free", Day: now.UTC().Format("2006-01-02")}},
	}}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.lastResults = oldResults
		s.mu.Unlock()
	}()
	updateUI(nil, nil, map[string]*providerMenuItems{"opencode_free": menu},
		systray.AddMenuItem("reauth", ""), newIconPicker(), systray.AddMenuItem("empty", ""), systray.AddMenuItem("setup", ""))
	if menu.statusState.visible {
		t.Fatalf("unused catalog candidate showed a free row: %+v", menu.statusState)
	}
}
