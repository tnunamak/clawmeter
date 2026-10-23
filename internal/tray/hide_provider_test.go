//go:build tray

package tray

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/status"
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
	showHidden *showHiddenMenu
	showReqs   chan string
	requests   chan string
	mu         sync.Mutex
}

func newHideFixture(t *testing.T, explicit bool, sources ...hideTestProvider) *hideFixture {
	t.Helper()
	// Each test has its own empty config and cache directories.
	scopeUserDirs(t.TempDir(), func(key, value string) error { t.Setenv(key, value); return nil })
	f := &hideFixture{t: t, menus: map[string]*providerMenuItems{}, picker: newIconPicker(), requests: make(chan string, 1), showReqs: make(chan string, 1)}
	for _, p := range sources {
		menu := createProviderMenuItems(p, explicit, nil, len(sources) > 1)
		menu.order = len(f.menus)
		f.menus[provider.SourceKey(p)] = menu
		forwardHide(menu, f.requests)
	}
	f.reauth = systray.AddMenuItem("reauth", "")
	f.empty = systray.AddMenuItem("empty", "")
	f.setup = systray.AddMenuItem("setup", "")
	f.showHidden = newShowHiddenMenu(systray.AddMenuItem("Settings", ""), f.menus)
	f.showHidden.forward(f.showReqs)
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
	f.showHidden.update(f.menus)
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

// showItem is the Show hidden providers entry of a family.
func (f *hideFixture) showItem(family string) *hiddenFamilyItem {
	for _, entry := range f.showHidden.items {
		if entry.family == family {
			return entry
		}
	}
	f.t.Fatalf("no Show hidden entry for %q", family)
	return nil
}

// clickShow presses a family's Show hidden entry and returns the family its
// handler sent to the main loop.
func (f *hideFixture) clickShow(family string) string {
	f.t.Helper()
	select {
	case f.showItem(family).item.ClickedCh <- struct{}{}:
	case <-time.After(time.Second):
		f.t.Fatal("click was not received")
	}
	select {
	case got := <-f.showReqs:
		return got
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
	if f.showHidden.parentState.visible {
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
	if !f.showHidden.parentState.visible || !f.showItem("openai").state.visible {
		t.Fatal("Show hidden providers is not offered after a provider is hidden")
	}
	if f.showItem("openai").state.title != "Codex" {
		t.Fatalf("entry title = %q", f.showItem("openai").state.title)
	}
}

func TestShowHiddenRestoresOnlyTheClickedProvider(t *testing.T) {
	kimi := hideTestProvider{sourceMenuTestProvider{name: "kimi", id: "default"}, "Kimi"}
	f := newHideFixture(t, true, codexSource(), kimi)
	codexKey, kimiKey := provider.SourceKey(codexSource()), provider.SourceKey(kimi)
	broken := func(name string) *provider.UsageData {
		return &provider.UsageData{Provider: name, FetchedAt: time.Now(), Error: "HTTP 500"}
	}
	f.draw(map[string]*provider.UsageData{codexKey: broken("openai"), kimiKey: broken("kimi")})
	// Config also turns off a provider with no menu; nothing may restore it.
	if _, err := config.SetProviderEnabled("ghost", false); err != nil {
		t.Fatal(err)
	}
	f.change(func() (*config.Config, error) { return saveHidden("openai") })
	f.change(func() (*config.Config, error) { return saveHidden("kimi") })
	if f.menus[codexKey].headerState.visible || f.menus[kimiKey].headerState.visible {
		t.Fatal("setup: both providers should be hidden")
	}
	if len(f.showHidden.items) != 2 || !f.showItem("openai").state.visible || !f.showItem("kimi").state.visible {
		t.Fatalf("entries: %+v", f.showHidden.items)
	}

	family := f.clickShow("openai")
	if !f.change(func() (*config.Config, error) { return saveShown(family) }) {
		t.Fatal("change failed")
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.IsProviderExplicitlyEnabled("openai") || !saved.IsProviderDisabled("kimi") || !saved.IsProviderDisabled("ghost") {
		t.Fatalf("one click changed other providers: %+v", saved.Providers)
	}
	if !f.menus[codexKey].headerState.visible || !f.menus[codexKey].hideState.visible {
		t.Fatalf("block did not return: header %+v hide %+v", f.menus[codexKey].headerState, f.menus[codexKey].hideState)
	}
	if f.menus[kimiKey].headerState.visible {
		t.Fatal("the other hidden provider came back")
	}
	if f.showItem("openai").state.visible || !f.showItem("kimi").state.visible || !f.showHidden.parentState.visible {
		t.Fatalf("entries after click: codex %+v kimi %+v parent %+v", f.showItem("openai").state, f.showItem("kimi").state, f.showHidden.parentState)
	}

	f.change(func() (*config.Config, error) { return saveShown("kimi") })
	if f.showHidden.parentState.visible {
		t.Fatal("submenu stays visible with no provider hidden (ghost has no menu)")
	}
}

// Before the first poll ends there is no reading; the block must not offer to
// hide a provider that has not failed.
func TestNoHideItemBeforeTheFirstPollEnds(t *testing.T) {
	f := newHideFixture(t, true, codexSource())
	menu := f.menus[provider.SourceKey(codexSource())]
	f.draw(nil)
	if !menu.headerState.visible {
		t.Fatal("setup: the explicitly enabled provider should be shown while it loads")
	}
	if menu.hideState.visible {
		t.Fatal("Hide offered before any check finished")
	}
}

func TestSignInSetupStatusOffersHide(t *testing.T) {
	menus := testMenus("tokenplan")
	needsAuth := map[string]provider.SetupStatus{"tokenplan": {State: provider.SetupNeedsAuth}}
	shown := map[string]*provider.UsageData{"tokenplan": nil}
	if !hideOffered("tokenplan", nil, menus, shown, needsAuth) {
		t.Fatal("no Hide for a provider that needs sign-in")
	}
	if hideOffered("tokenplan", nil, menus, shown, nil) {
		t.Fatal("Hide offered with no reading and no setup status")
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

// failingProvider is a configured provider whose every check ends in an error.
type failingProvider struct{ hideTestProvider }

func (p failingProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return &provider.UsageData{Provider: p.Name(), FetchedAt: time.Now(), Error: "quota access expired"}, nil
}

// refreshFixture runs the real refresh path (registry, config reload, fetch,
// render) over one failing provider.
func newRefreshFixture(t *testing.T) (*hideFixture, *usageRefresher, *providerMenuItems) {
	t.Helper()
	p := failingProvider{hideTestProvider{sourceMenuTestProvider{name: "alibaba_token", id: "default"}, "Alibaba Token Plan"}}
	f := newHideFixture(t, false, p.hideTestProvider)
	menu := f.menus[provider.SourceKey(p)]
	s.mu.Lock()
	oldGate := s.failureGate
	s.failureGate = provider.NewFailureGate()
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.failureGate = oldGate
		s.mu.Unlock()
	})
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	reload := func() {
		saved, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		f.apply(saved)
		registry.SetEnabledFilter(saved)
	}
	r := &usageRefresher{
		registry:     registry,
		menus:        f.menus,
		reloadConfig: reload,
		pollSeconds:  func() int { return 300 },
		render:       func(map[string]*provider.UsageData, map[string]*status.ProviderStatus) { f.render() },
		finished:     func(time.Time) {},
	}
	reload()
	return f, r, menu
}

// A provider shown again returns to its real state, an error with Hide, after
// one refresh, even when a refresh that began before the click is still running.
func TestShownAgainProviderReturnsToItsRealState(t *testing.T) {
	f, r, menu := newRefreshFixture(t)
	saveAndApply := func(save func() (*config.Config, error)) {
		t.Helper()
		if !f.change(save) {
			t.Fatal("change failed")
		}
	}
	if !r.refresh(false) || !menu.hideState.visible {
		t.Fatalf("setup: error not shown with Hide: header %+v hide %+v", menu.headerState, menu.hideState)
	}
	saveAndApply(func() (*config.Config, error) { return saveHidden("alibaba_token") })
	r.refresh(false)
	if menu.headerState.visible {
		t.Fatal("setup: provider still shown after Hide")
	}

	// A refresh that read the config before the click is still running.
	r.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		showHiddenProvider("alibaba_token", &f.mu, f.apply, f.render, r.refreshWhenFree)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		trayRenderMu.Lock()
		hidden := menu.disabled
		trayRenderMu.Unlock()
		if !hidden {
			break
		}
		time.Sleep(time.Millisecond)
	}
	r.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("show handler did not finish")
	}

	if !menu.headerState.visible || !strings.Contains(menu.headerState.title, "quota access expired") {
		t.Fatalf("block did not return to its error: header %+v", menu.headerState)
	}
	if !menu.hideState.visible {
		t.Fatal("no Hide item on the returned error")
	}
}
