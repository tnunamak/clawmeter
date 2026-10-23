//go:build tray

package tray

import (
	"sync"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
)

// hideTestProvider is a source of a provider family with its own display name.
type hideTestProvider struct {
	sourceMenuTestProvider
	display string
}

func (p hideTestProvider) DisplayName() string { return p.display }

// hideFixture is a tray menu with the shared items, drawn from s.lastResults.
type hideFixture struct {
	t          *testing.T
	menus      map[string]*providerMenuItems
	reauth     *systray.MenuItem
	empty      *systray.MenuItem
	setup      *systray.MenuItem
	picker     *iconPicker
	showHidden *systray.MenuItem
	showState  menuItemState
	requests   chan string
	mu         sync.Mutex
}

func newHideFixture(t *testing.T, explicit bool, sources ...hideTestProvider) *hideFixture {
	t.Helper()
	// Each test has its own empty config and cache directories.
	scopeUserDirs(t.TempDir(), func(key, value string) error { t.Setenv(key, value); return nil })
	f := &hideFixture{t: t, menus: map[string]*providerMenuItems{}, picker: newIconPicker(), requests: make(chan string, 1)}
	for _, p := range sources {
		menu := createProviderMenuItems(p, explicit, nil, len(sources) > 1)
		menu.order = len(f.menus)
		f.menus[provider.SourceKey(p)] = menu
		forwardHide(menu, f.requests)
	}
	f.reauth = systray.AddMenuItem("reauth", "")
	f.empty = systray.AddMenuItem("empty", "")
	f.setup = systray.AddMenuItem("setup", "")
	f.showHidden = systray.AddMenuItem("Show hidden providers", "")
	f.showHidden.Hide()
	f.showState = menuItemState{visible: false, enabled: true, initialized: true}
	s.mu.Lock()
	oldResults, oldStatuses, oldCfg := s.lastResults, s.statuses, cfg
	s.statuses = nil
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.lastResults, s.statuses, cfg = oldResults, oldStatuses, oldCfg
		s.mu.Unlock()
		systray.ResetMenu()
	})
	return f
}

func (f *hideFixture) draw(results map[string]*provider.UsageData) {
	s.mu.Lock()
	s.lastResults = results
	s.mu.Unlock()
	f.render()
}

func (f *hideFixture) render() {
	updateUI(nil, nil, f.menus, f.reauth, f.picker, f.empty, f.setup)
}

// apply is what Run does with a saved config.
func (f *hideFixture) apply(newCfg *config.Config) {
	s.mu.Lock()
	cfg = newCfg
	s.mu.Unlock()
	applyProviderEnablement(f.menus, newCfg)
	trayRenderMu.Lock()
	updateShowHidden(f.showHidden, &f.showState, f.menus)
	trayRenderMu.Unlock()
}

// change runs a save as a click handler does.
func (f *hideFixture) change(save func() (*config.Config, error)) bool {
	return changeProviderEnablement(&f.mu, save, f.apply, f.render)
}

// clickHide presses a block's Hide item and returns the family its handler
// sent to the main loop.
func (f *hideFixture) clickHide(menu *providerMenuItems) string {
	f.t.Helper()
	select {
	case menu.hideItem.ClickedCh <- struct{}{}:
	case <-time.After(time.Second):
		f.t.Fatal("click was not received")
	}
	select {
	case family := <-f.requests:
		return family
	case <-time.After(time.Second):
		f.t.Fatal("click did not reach the request channel")
	}
	return ""
}

func healthyReading(name string) *provider.UsageData {
	now := time.Now()
	return &provider.UsageData{Provider: name, FetchedAt: now, Windows: []provider.UsageWindow{
		{Name: "5h", Length: 5 * time.Hour, Utilization: 10, ResetsAt: now.Add(4 * time.Hour)},
	}}
}

func codexSource() hideTestProvider {
	return hideTestProvider{sourceMenuTestProvider{name: "openai", id: "default"}, "Codex"}
}

func TestHideItemAppearsOnlyForAProblem(t *testing.T) {
	f := newHideFixture(t, true, codexSource())
	key := provider.SourceKey(codexSource())
	menu := f.menus[key]

	cases := []struct {
		name string
		data *provider.UsageData
		want bool
	}{
		{"healthy", healthyReading("openai"), false},
		{"error", &provider.UsageData{Provider: "openai", FetchedAt: time.Now(), Error: "HTTP 500"}, true},
		{"expired login", &provider.UsageData{Provider: "openai", FetchedAt: time.Now(), IsExpired: true}, true},
		{"needs sign-in", nil, true},
		{"healthy again", healthyReading("openai"), false},
	}
	for _, c := range cases {
		results := map[string]*provider.UsageData{}
		if c.data != nil {
			results[key] = c.data
		}
		f.draw(results)
		if menu.hideState.visible != c.want {
			t.Fatalf("%s: Hide visible = %v, want %v", c.name, menu.hideState.visible, c.want)
		}
	}
	if menu.hideState.title != "Hide Codex" {
		t.Fatalf("title = %q", menu.hideState.title)
	}
}

func TestHideClickWritesConfigAndHidesTheBlock(t *testing.T) {
	f := newHideFixture(t, true, codexSource())
	key := provider.SourceKey(codexSource())
	menu := f.menus[key]
	f.draw(map[string]*provider.UsageData{key: {Provider: "openai", FetchedAt: time.Now(), Error: "HTTP 500"}})
	if !menu.hideState.visible || !menu.headerState.visible {
		t.Fatalf("setup: Hide %+v header %+v", menu.hideState, menu.headerState)
	}
	if f.showState.visible {
		t.Fatal("Show hidden providers is visible with nothing hidden")
	}

	family := f.clickHide(menu)
	if family != "openai" {
		t.Fatalf("family = %q", family)
	}
	if !f.change(func() (*config.Config, error) { return saveHidden(family) }) {
		t.Fatal("change failed")
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.IsProviderDisabled("openai") {
		t.Fatalf("config does not disable the provider: %+v", saved.Providers)
	}
	if menu.headerState.visible || menu.statusState.visible || menu.hideState.visible {
		t.Fatalf("block still shown: header %+v status %+v hide %+v", menu.headerState, menu.statusState, menu.hideState)
	}
	if !f.showState.visible {
		t.Fatal("Show hidden providers is not offered after a provider is hidden")
	}
}

func TestShowHiddenProvidersRestoresThem(t *testing.T) {
	f := newHideFixture(t, true, codexSource())
	key := provider.SourceKey(codexSource())
	menu := f.menus[key]
	f.draw(map[string]*provider.UsageData{key: {Provider: "openai", FetchedAt: time.Now(), Error: "HTTP 500"}})
	f.change(func() (*config.Config, error) { return saveHidden("openai") })
	if menu.headerState.visible || !f.showState.visible {
		t.Fatalf("setup: header %+v, show hidden %+v", menu.headerState, f.showState)
	}

	if !f.change(saveShowHidden) {
		t.Fatal("change failed")
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.IsProviderDisabled("openai") || !saved.IsProviderExplicitlyEnabled("openai") {
		t.Fatalf("config still disables the provider: %+v", saved.Providers)
	}
	if !menu.headerState.visible || !menu.hideState.visible {
		t.Fatalf("block did not return: header %+v hide %+v", menu.headerState, menu.hideState)
	}
	if f.showState.visible {
		t.Fatal("Show hidden providers stays visible with nothing hidden")
	}
}

func TestAFamilyWithAHealthySiblingGetsNoHideItem(t *testing.T) {
	work := hideTestProvider{sourceMenuTestProvider{name: "claude", id: "work"}, "Claude"}
	home := hideTestProvider{sourceMenuTestProvider{name: "claude", id: "home"}, "Claude"}
	f := newHideFixture(t, true, work, home)
	workKey, homeKey := provider.SourceKey(work), provider.SourceKey(home)
	broken := &provider.UsageData{Provider: "claude", SourceID: "work", FetchedAt: time.Now(), Error: "HTTP 500"}
	healthy := healthyReading("claude")
	healthy.SourceID = "home"

	f.draw(map[string]*provider.UsageData{workKey: broken, homeKey: healthy})
	if !f.menus[workKey].headerState.visible || !f.menus[homeKey].headerState.visible {
		t.Fatal("setup: both accounts should be shown")
	}
	if f.menus[workKey].hideState.visible || f.menus[homeKey].hideState.visible {
		t.Fatalf("Hide offered beside a healthy account: work %+v home %+v", f.menus[workKey].hideState, f.menus[homeKey].hideState)
	}

	// With no healthy account left, hiding the family loses nothing.
	expired := &provider.UsageData{Provider: "claude", SourceID: "home", FetchedAt: time.Now(), IsExpired: true}
	f.draw(map[string]*provider.UsageData{workKey: broken, homeKey: expired})
	if !f.menus[workKey].hideState.visible || !f.menus[homeKey].hideState.visible {
		t.Fatalf("Hide missing when every account has a problem: work %+v home %+v", f.menus[workKey].hideState, f.menus[homeKey].hideState)
	}
	if got := f.clickHide(f.menus[workKey]); got != "claude" {
		t.Fatalf("family = %q", got)
	}
	if f.menus[workKey].hideState.title != "Hide Claude" {
		t.Fatalf("title = %q", f.menus[workKey].hideState.title)
	}
}
