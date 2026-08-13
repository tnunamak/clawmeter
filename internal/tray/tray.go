//go:build tray

package tray

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/systray"
	"github.com/gen2brain/beeep"
	"github.com/pkg/browser"

	"github.com/tnunamak/clawmeter/internal/autostart"
	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	"github.com/tnunamak/clawmeter/internal/shellpath"
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
	"github.com/tnunamak/clawmeter/internal/update"
)

const (
	updateCheckInterval    = 30 * time.Minute
	tokenPlanProviderName  = "alibaba_token"
	tokenPlanConnectTarget = "token-plan"
)

func pollIntervalForConfig(seconds int) time.Duration {
	interval := time.Duration(seconds) * time.Second
	minimum := time.Duration(config.MinimumPollIntervalSeconds) * time.Second
	if interval < minimum {
		return minimum
	}
	return interval
}

type state struct {
	mu                        sync.Mutex
	lastResults               map[string]*provider.UsageData
	lastSourceRevisions       map[string]string
	statuses                  map[string]*status.ProviderStatus
	failureGate               *provider.FailureGate
	pendingRelease            *update.Release
	currentTitle              string
	currentTooltip            string
	lastRefreshAt             time.Time
	iconAutoMode              iconAutoMode
	iconTargetOverride        iconTarget
	iconTargetChoices         []iconTarget
	iconTargetState           menuItemState
	iconStateKnown            bool
	currentIconProvider       string
	currentIconMeter          icons.MeterState
	reauthVisible             bool
	reauthVisibleKnown        bool
	emptyVisible              bool
	emptyVisibleKnown         bool
	providerSetupVisible      bool
	providerSetupVisibleKnown bool
}

type iconTarget struct {
	Provider string
	Window   string
}

type iconAutoMode string

const (
	iconAutoRisk      iconAutoMode = "risk"
	iconAutoProjected iconAutoMode = "projected"
	iconAutoRunway    iconAutoMode = "runway"
)

var (
	s                state
	version          string
	cfg              *config.Config
	iconClickActions chan iconClickAction
	// systray's Linux implementation performs a synchronous D-Bus refresh for
	// every menu mutation. Keep background refreshes and click handlers from
	// mutating the native menu concurrently.
	trayRenderMu sync.Mutex
)

func Run(ver string) int {
	redirectLogToFile()
	configureNotificationIdentity()
	version = ver
	iconClickActions = make(chan iconClickAction, 1)
	installTrayClickHandlers(iconClickActions)
	// Login-shell PATH capture can execute user startup files and take several
	// seconds. Start it before provider refresh, but never make Plasma wait for
	// it to register the tray menu. Providers that need the recovered PATH call
	// Init through their executable lookup and wait on the same sync.Once.
	go shellpath.Init()
	setupIconTheme()
	systray.Run(onReady, func() {
		cleanupIconTheme()
	})
	return 0
}

func configureNotificationIdentity() {
	beeep.AppName = "Clawmeter"
}

func onReady() {
	s.failureGate = provider.NewFailureGate()

	var err error
	cfg, err = config.Load(all.SourceValidator())
	if err != nil {
		setErrorState("Config error")
		return
	}

	// Build the initial native menu as one transaction. On Linux, each menu
	// mutation otherwise emits a separate D-Bus layout signal while Plasma is
	// trying to register the tray item.
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()

	// Launch at login is opt-in: the user enables it from the tray menu.
	// Do not silently persist anything on first run.

	setIconByName("gray", icons.Gray)
	systray.SetTitle("Clawmeter")
	systray.SetTooltip("")

	// Build header
	mHeader := systray.AddMenuItem(fmt.Sprintf("Clawmeter %s", version), "")
	mHeader.Disable()
	systray.AddSeparator()

	// Create registry and register providers
	registry := provider.NewRegistry()
	all.Register(registry, cfg, shellpath.NewSessionEnvironmentResolver())

	// Build a menu group for every registered provider, ordered
	// deterministically. The systray library can't insert menu items between
	// existing ones, so we must pre-allocate every slot. updateUI() shows or
	// hides each group based on the provider's current state (configured,
	// has data, errored, etc.) — meaning a provider that becomes available
	// after launch (e.g. user runs `codex login` after starting the tray)
	// appears on the next refresh without needing a restart.
	providerMenus := make(map[string]*providerMenuItems)
	providerConnectActions := make(chan string, 1)
	familyCounts := make(map[string]int)
	for _, p := range registry.GetAll() {
		familyCounts[p.Name()]++
	}
	for _, p := range registry.GetAll() {
		explicit := cfg.IsProviderExplicitlyEnabled(p.Name())
		menu := createProviderMenuItems(p, explicit, providerConnectActions, familyCounts[p.Name()] > 1)
		hideProviderMenu(menu)
		providerMenus[provider.SourceKey(p)] = menu
	}

	// Placeholder shown when no provider is in the primary UI; updateUI
	// toggles its visibility based on whether any menu has data to render.
	mEmpty := systray.AddMenuItem("No active providers", "")
	mEmpty.Disable()
	mProviderSetup := systray.AddMenuItem("Manage providers: run `clawmeter providers`", "")
	mProviderSetup.Disable()
	mProviderSetup.Hide()
	s.emptyVisible = true
	s.emptyVisibleKnown = true
	s.providerSetupVisible = false
	s.providerSetupVisibleKnown = true

	systray.AddSeparator()

	// Global menu items
	mIconProvider := systray.AddMenuItem("Icon: Auto (click to cycle)", "")
	s.iconTargetState = menuItemState{title: "Icon: Auto (click to cycle)", enabled: true, initialized: true}
	mIconAutoMode := systray.AddMenuItem("Auto Mode: Risk", "")
	mRefresh := systray.AddMenuItem("Refresh Now", "")
	systray.AddSeparator()
	iconActionCh := iconClickActions
	if iconActionCh == nil {
		iconActionCh = make(chan iconClickAction, 1)
	}

	// Hint shown only when Claude's OAuth token has expired. We don't try
	// to launch a terminal for the user — Windows has no obvious "default
	// terminal" we can rely on, and the macOS/Linux versions of that flow
	// saved at most one keystroke. A disabled menu item is the same
	// outcome with zero platform-specific code.
	mReauth := systray.AddMenuItem("Claude token expired — run `claude` to reauth", "")
	mReauth.Disable()
	mReauth.Hide()
	s.reauthVisible = false
	s.reauthVisibleKnown = true

	var updateChecking sync.Mutex
	mUpdate := systray.AddMenuItem("", "")
	mUpdate.Hide()

	systray.AddSeparator()
	mAutostart := systray.AddMenuItem("", "")
	updateAutostartLabel(mAutostart)
	mQuit := systray.AddMenuItem("Quit", "")

	// Guard against concurrent refreshes
	var refreshing sync.Mutex

	// reloadConfig re-reads config.yaml from disk and propagates changes
	// to the registry filter and per-provider menu state. This is what
	// makes `clawmeter config enable <provider>` (or any other out-of-band
	// edit) reflect in the running tray without a restart.
	reloadConfig := func() {
		newCfg, err := config.Load(all.SourceValidator())
		if err != nil {
			return
		}
		cfg = newCfg
		registry.SetEnabledFilter(cfg)
		applyProviderEnablement(providerMenus, cfg)
	}

	// Refresh usage only (lightweight, runs every poll cycle)
	refresh := func(force bool) {
		if !refreshing.TryLock() {
			return // already refreshing
		}
		defer refreshing.Unlock()

		reloadConfig()

		// Refresh already runs off the tray event loop. Complete executable-path
		// recovery before discovering configured providers so CLI-backed sources
		// cannot disappear based on a startup race.
		shellpath.Init()
		configured := registry.GetConfigured()
		currentRevisions := providerSourceRevisions(configured)
		s.mu.Lock()
		priorResults := resultsMatchingSourceRevisions(s.lastResults, s.lastSourceRevisions, currentRevisions)
		toFetch, skipped := splitProvidersForRefresh(configured, s.failureGate, priorResults, force)
		priorRevisions := cloneSourceRevisions(s.lastSourceRevisions)
		s.mu.Unlock()

		// Credential discovery may recover allowlisted variables through a login
		// shell. Start the network deadline afterward so discovery latency cannot
		// leave every provider with an already-expired context.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result := provider.FetchProvidersParallel(ctx, toFetch)

		// Merge in cached data for backed-off providers
		for name, cached := range skipped {
			result.Results[name] = cached
			if revision := priorRevisions[name]; revision != "" {
				result.SourceRevisions[name] = revision
			}
		}

		// Apply failure gate: suppress transient errors when cached data exists
		s.mu.Lock()
		for name, data := range result.Results {
			if _, wasSkipped := skipped[name]; wasSkipped {
				continue // already using cached data
			}
			if data == nil {
				continue
			}
			if data.Error != "" {
				log.Printf("provider refresh failed: provider=%s error=%s", name, data.Error)
			}

			if data.Error == "" && !data.IsExpired {
				s.failureGate.RecordSuccess(name)
				continue
			}
			// Provider errored — keep showing last good windows, but label them
			// stale so the tray never turns unavailable data into a clean 0%.
			hasPrior := false
			if prev, ok := priorResults[name]; ok && prev != nil && prev.HasPresentableUsage() && cache.SourceRevisionMatches(priorRevisions, name, result.SourceRevisions[name]) {
				hasPrior = true
			}
			if data.InvalidatesPriorUsage {
				_ = s.failureGate.ShouldSurfaceError(name, hasPrior)
			} else if hasPrior && !data.HasPresentableUsage() {
				prev := priorResults[name].Clone()
				prev.MarkStale(data.Error)
				result.Results[name] = prev
				_ = s.failureGate.ShouldSurfaceError(name, true)
			} else if !s.failureGate.ShouldSurfaceError(name, hasPrior) {
				// First failure with prior data — keep showing cache silently.
				if prev, ok := priorResults[name]; ok && prev != nil && cache.SourceRevisionMatches(priorRevisions, name, result.SourceRevisions[name]) {
					cached := prev.Clone()
					cached.MarkStale(data.Error)
					result.Results[name] = cached
				}
			}
		}
		s.mu.Unlock()

		_ = cache.Write(result)

		now := time.Now()
		s.mu.Lock()
		s.lastResults = result.Results
		s.lastSourceRevisions = result.SourceRevisions
		s.lastRefreshAt = now
		statuses := s.statuses // reuse last known statuses
		s.mu.Unlock()

		updateUI(result.Results, statuses, providerMenus, mReauth, mIconProvider, mEmpty, mProviderSetup)
		trayRenderMu.Lock()
		mRefresh.SetTitle(fmt.Sprintf("Refresh Now  (updated %s)", now.Format("15:04")))
		trayRenderMu.Unlock()
	}

	// Refresh status pages (heavier, runs less often)
	refreshStatus := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		shellpath.Init()
		statuses := status.FetchAll(ctx, providerNames(registry.GetConfigured()))

		s.mu.Lock()
		s.statuses = statuses
		lastResults := s.lastResults
		s.mu.Unlock()

		if lastResults != nil {
			updateUI(lastResults, statuses, providerMenus, mReauth, mIconProvider, mEmpty, mProviderSetup)
		}
	}

	// Check update function
	checkUpdate := func() {
		if !cfg.ShouldCheckForUpdates() {
			setPendingRelease(nil)
			trayRenderMu.Lock()
			mUpdate.Hide()
			trayRenderMu.Unlock()
			return
		}
		if !updateChecking.TryLock() {
			return
		}
		defer updateChecking.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rel, err := update.Check(ctx, version)
		if err != nil {
			return
		}
		setPendingRelease(rel)
		trayRenderMu.Lock()
		if rel == nil {
			mUpdate.Hide()
		} else {
			mUpdate.SetTitle(fmt.Sprintf("• Update to %s", rel.Version))
			mUpdate.Show()
		}
		s.mu.Lock()
		results := s.lastResults
		s.mu.Unlock()
		if results != nil {
			displayNames := providerDisplayNames(providerMenus)
			updateTrayIcon(results)
			updateTrayTitle(results)
			updateTrayTooltip(results, displayNames)
		}
		trayRenderMu.Unlock()
	}

	// Apply update function
	applyUpdate := func() {
		rel := currentPendingRelease()
		if rel == nil {
			return
		}
		trayRenderMu.Lock()
		mUpdate.SetTitle("Updating...")
		mUpdate.Disable()
		trayRenderMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		exe, err := update.ExecutablePath()
		if err != nil {
			trayRenderMu.Lock()
			mUpdate.SetTitle(fmt.Sprintf("Update failed: %v", err))
			mUpdate.Enable()
			trayRenderMu.Unlock()
			return
		}
		if err := update.ApplyTo(ctx, rel.URL, exe); err != nil {
			trayRenderMu.Lock()
			mUpdate.SetTitle(fmt.Sprintf("Update failed: %v", err))
			mUpdate.Enable()
			trayRenderMu.Unlock()
			return
		}
		notify("Clawmeter", fmt.Sprintf("Updated to %s — restarting", rel.Version), "low")
		if err := update.Restart(exe); err != nil {
			trayRenderMu.Lock()
			mUpdate.SetTitle("Updated — restart Clawmeter")
			mUpdate.Enable()
			trayRenderMu.Unlock()
			notify("Clawmeter", "Updated, but restart failed. Restart Clawmeter manually.", "normal")
			return
		}
		systray.Quit()
	}

	// Show cached data immediately while fetching fresh data
	if cached, err := cache.Read(); err == nil && cached != nil {
		// Do not probe credentials while the native menu is being registered.
		// GetConfigured calls every provider's IsConfigured method, and some
		// providers recover PATH through a login shell. That work belongs to the
		// background refresh; cached rendering only needs the registered names.
		allProviders := registry.GetAll()
		filtered := cachedResultsForCurrentSources(cached, allProviders)
		s.mu.Lock()
		s.lastResults = filtered
		s.lastSourceRevisions = providerSourceRevisions(allProviders)
		s.lastRefreshAt = cached.FetchedAt
		s.mu.Unlock()
		updateUI(filtered, nil, providerMenus, mReauth, mIconProvider, mEmpty, mProviderSetup)
	}

	// Initial refresh in background (don't block tray UI)
	go refresh(false)
	go refreshStatus()

	// Check for updates on startup
	go checkUpdate()

	// Setup tickers
	pollInterval := pollIntervalForConfig(cfg.Settings.PollInterval)
	ticker := time.NewTicker(pollInterval)
	statusTicker := time.NewTicker(15 * time.Minute) // status pages checked less often
	updateTicker := time.NewTicker(updateCheckInterval)

	// Event loop (never blocks on network I/O)
	go func() {
		for {
			select {
			case <-ticker.C:
				go refresh(false)
			case <-statusTicker.C:
				go refreshStatus()
			case <-updateTicker.C:
				go checkUpdate()
			case <-mRefresh.ClickedCh:
				go refresh(true)
				go checkUpdate()
			case action := <-iconActionCh:
				if action == iconClickResetAuto {
					resetIconSelection(providerMenus, mIconProvider, mIconAutoMode)
				} else {
					cycleIconSelection(providerMenus, mIconProvider)
				}
			case <-mIconProvider.ClickedCh:
				cycleIconSelection(providerMenus, mIconProvider)
			case <-mIconAutoMode.ClickedCh:
				toggleIconAutoMode(providerMenus, mIconProvider, mIconAutoMode)
			case providerName := <-providerConnectActions:
				menu := providerMenus[providerName]
				if menu == nil || menu.connectItem == nil {
					continue
				}
				if !menu.connecting.CompareAndSwap(false, true) {
					continue
				}
				trayRenderMu.Lock()
				setProviderConnectionItemLocked(menu, "", true)
				trayRenderMu.Unlock()
				go func(providerName string, menu *providerMenuItems) {
					defer menu.connecting.Store(false)
					if err := connectProviderFromTray(providerName); err != nil {
						trayRenderMu.Lock()
						setProviderConnectionItemLocked(menu, "Retry quota access", true)
						trayRenderMu.Unlock()
						notify(menu.provider.DisplayName(), err.Error(), "normal")
						return
					}
					notify(menu.provider.DisplayName(), "Quota access connected. Refreshing...", "low")
					go refresh(true)
					go refreshStatus()
				}(providerName, menu)
			case <-mUpdate.ClickedCh:
				// Download, verify, replace, and restart can take seconds. Keep
				// the tray event loop available while the update runs.
				go applyUpdate()
			case <-mAutostart.ClickedCh:
				go toggleAutostart(mAutostart)
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func applyProviderEnablement(menus map[string]*providerMenuItems, cfg *config.Config) {
	for _, menu := range menus {
		menu.explicitlyEnabled = cfg.IsProviderExplicitlyEnabled(menu.provider.Name())
	}
}

// providerMenuItems holds menu items for a single provider.
type providerMenuItems struct {
	provider          provider.Provider
	displayName       string
	headerItem        *systray.MenuItem
	statusItem        *systray.MenuItem
	connectItem       *systray.MenuItem
	windowItems       []*systray.MenuItem
	balanceItems      []*systray.MenuItem
	dashboardItem     *systray.MenuItem
	everHealthy       bool // true once we've seen useful quota data
	explicitlyEnabled bool
	connecting        atomic.Bool
	headerState       menuItemState
	statusState       menuItemState
	connectState      menuItemState
	windowStates      []menuItemState
	balanceStates     []menuItemState
	dashboardState    menuItemState
}

type menuItemState struct {
	title       string
	visible     bool
	enabled     bool
	initialized bool
}

const maxWindowItems = 8 // pre-allocate up to 8 window slots per provider

func createProviderMenuItems(p provider.Provider, explicitlyEnabled bool, connectActions chan<- string, repeated bool) *providerMenuItems {
	displayName := p.DisplayName()
	label := provider.SourceLabel(p)
	if label == "" {
		label = provider.SourceID(p)
	}
	if repeated && label != "" {
		displayName += " · " + label
	}
	// Provider header (disabled)
	header := systray.AddMenuItem(displayName, "")
	header.Disable()

	// Status item (shows loading/error)
	statusItem := systray.AddMenuItem("Loading...", "")
	statusItem.Disable()

	var connectItem *systray.MenuItem
	if p.Name() == tokenPlanProviderName && connectActions != nil {
		connectItem = systray.AddMenuItem("Connect quota access", "")
		go func() {
			for range connectItem.ClickedCh {
				select {
				case connectActions <- p.Name():
				default:
				}
			}
		}()
	}

	// Pre-create window items (hidden until populated)
	windowItems := make([]*systray.MenuItem, maxWindowItems)
	for i := range windowItems {
		item := systray.AddMenuItem("", "")
		item.Disable()
		item.Hide()
		windowItems[i] = item
	}
	windowStates := make([]menuItemState, len(windowItems))
	for i := range windowStates {
		windowStates[i] = menuItemState{visible: false, enabled: false, initialized: true}
	}
	balanceItems := make([]*systray.MenuItem, maxWindowItems)
	for i := range balanceItems {
		item := systray.AddMenuItem("", "")
		item.Disable()
		item.Hide()
		balanceItems[i] = item
	}
	balanceStates := make([]menuItemState, len(balanceItems))
	for i := range balanceStates {
		balanceStates[i] = menuItemState{visible: false, enabled: false, initialized: true}
	}

	// Dashboard item (clickable). The disabled-header of the next provider
	// (or the global separator after the provider block) acts as the visual
	// divider; we don't add a per-provider separator because there's no way
	// to hide a separator and the provider block has variable membership.
	dashboardItem := systray.AddMenuItem(fmt.Sprintf("Open %s Dashboard", p.DisplayName()), p.DashboardURL())
	go func() {
		for range dashboardItem.ClickedCh {
			openURL(p.DashboardURL())
		}
	}()

	return &providerMenuItems{
		provider:          p,
		displayName:       displayName,
		headerItem:        header,
		statusItem:        statusItem,
		connectItem:       connectItem,
		windowItems:       windowItems,
		balanceItems:      balanceItems,
		dashboardItem:     dashboardItem,
		explicitlyEnabled: explicitlyEnabled,
		headerState:       menuItemState{title: displayName, visible: true, enabled: false, initialized: true},
		statusState:       menuItemState{title: "Loading...", visible: true, enabled: false, initialized: true},
		connectState:      menuItemState{title: "Connect quota access", visible: true, enabled: true, initialized: connectItem != nil},
		windowStates:      windowStates,
		balanceStates:     balanceStates,
		dashboardState:    menuItemState{title: fmt.Sprintf("Open %s Dashboard", p.DisplayName()), visible: true, enabled: true, initialized: true},
	}
}

func hideProviderMenu(menu *providerMenuItems) {
	setMenuItemVisible(menu.headerItem, &menu.headerState, false)
	setMenuItemVisible(menu.statusItem, &menu.statusState, false)
	if menu.connectItem != nil {
		setMenuItemVisible(menu.connectItem, &menu.connectState, false)
	}
	hideProviderWindows(menu)
	setMenuItemVisible(menu.dashboardItem, &menu.dashboardState, false)
}

func hideProviderWindows(menu *providerMenuItems) {
	for i, w := range menu.windowItems {
		setMenuItemVisible(w, &menu.windowStates[i], false)
	}
	for i, b := range menu.balanceItems {
		setMenuItemVisible(b, &menu.balanceStates[i], false)
	}
}

func showProviderHeader(menu *providerMenuItems) {
	setMenuItemVisible(menu.headerItem, &menu.headerState, true)
}

func setMenuItemTitle(item *systray.MenuItem, state *menuItemState, title string) {
	if !state.initialized || state.title != title {
		item.SetTitle(title)
		state.title = title
		state.initialized = true
	}
}

func setMenuItemVisible(item *systray.MenuItem, state *menuItemState, visible bool) {
	if !state.initialized || state.visible != visible {
		if visible {
			item.Show()
		} else {
			item.Hide()
		}
		state.visible = visible
		state.initialized = true
	}
}

func setMenuItemEnabled(item *systray.MenuItem, state *menuItemState, enabled bool) {
	if !state.initialized || state.enabled != enabled {
		if enabled {
			item.Enable()
		} else {
			item.Disable()
		}
		state.enabled = enabled
		state.initialized = true
	}
}

func setSharedMenuItemVisible(item *systray.MenuItem, visible bool, current *bool, known *bool) {
	if !*known || *current != visible {
		if visible {
			item.Show()
		} else {
			item.Hide()
		}
		*current = visible
		*known = true
	}
}

func providerConnectionMenuState(name string, data *provider.UsageData, explicitlyEnabled, setupReady bool) (status, action string, show bool) {
	if name != tokenPlanProviderName {
		return "", "", false
	}
	if data == nil && !setupReady {
		return "Quota access not connected", "Connect quota access", true
	}
	if data == nil || !data.IsExpired {
		return "", "", false
	}
	if strings.Contains(strings.ToLower(data.Error), "not connected") {
		return "Quota access not connected", "Connect quota access", true
	}
	return "Quota access expired", "Reconnect quota access", true
}

func setProviderConnectionItemLocked(menu *providerMenuItems, action string, show bool) {
	if menu.connectItem == nil {
		return
	}
	if menu.connecting.Load() {
		setMenuItemTitle(menu.connectItem, &menu.connectState, "Connecting...")
		setMenuItemEnabled(menu.connectItem, &menu.connectState, false)
		setMenuItemVisible(menu.connectItem, &menu.connectState, true)
		return
	}
	if !show {
		setMenuItemVisible(menu.connectItem, &menu.connectState, false)
		return
	}
	setMenuItemTitle(menu.connectItem, &menu.connectState, action)
	setMenuItemEnabled(menu.connectItem, &menu.connectState, true)
	setMenuItemVisible(menu.connectItem, &menu.connectState, true)
}

func connectProviderFromTray(providerName string) error {
	if providerName != tokenPlanProviderName {
		return fmt.Errorf("quota connection is not available for %s", providerName)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "providers", "connect", tokenPlanConnectTarget, "--force")
	var stdout, stderr bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Alibaba authorization timed out. Try again in a terminal.")
		}
		return fmt.Errorf("%s", providerConnectFailureMessage(stdout.String(), stderr.String()))
	}
	return nil
}

func providerConnectFailureMessage(stdout, stderr string) string {
	output := strings.ToLower(stdout + "\n" + stderr)
	switch {
	case strings.Contains(output, "official bailian cli is not installed") ||
		strings.Contains(output, "command not found: bl"):
		return "Bailian CLI is missing. Install Node.js 22.12+ and run `npm install --global bailian-cli`, then retry."
	case strings.Contains(output, "no console session was found"):
		return "Alibaba authorization finished without a quota session. Complete `bl auth login --console` in a terminal, then retry."
	case strings.Contains(output, "authorization did not complete"):
		return "Alibaba authorization did not complete. Retry from a terminal so its browser-login details are visible."
	default:
		return "Alibaba quota access could not be connected. Retry from a terminal for details."
	}
}

func updateUI(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus, menus map[string]*providerMenuItems, mReauth *systray.MenuItem, mIconProvider *systray.MenuItem, mEmpty *systray.MenuItem, mProviderSetup *systray.MenuItem) {
	setups := make(map[string]provider.SetupStatus, len(menus))
	for name, menu := range menus {
		// Only Alibaba has a setup-only menu path today. Other providers are
		// represented by their fetched data, and asking every provider for
		// setup status here can trigger multiple shell-environment probes while
		// the native menu is still being constructed.
		if name == tokenPlanProviderName {
			// Setup discovery can recover a shell environment and therefore may
			// take a bounded amount of time. Do it before taking the native-menu
			// mutation lock so a slow shell cannot stall tray interactions.
			setups[name] = provider.GetSetupStatus(menu.provider)
		}
	}

	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()

	hasExpiredClaude := false
	displayNames := make(map[string]string, len(menus))
	activeResults := make(map[string]*provider.UsageData, len(results))
	visibleProviderCount := 0

	for name, menu := range menus {
		displayNames[name] = menu.displayName
		data := results[name]

		if data != nil && data.EstablishesPrimaryUIHistory() {
			menu.everHealthy = true
		}

		setup := setups[name]
		detected := data != nil
		if name == tokenPlanProviderName {
			detected = detected || setup.State != provider.SetupUnavailable
		}
		if !provider.ShouldShowInPrimaryUI(data, menu.everHealthy, menu.explicitlyEnabled) && !detected {
			hideProviderMenu(menu)
			continue
		}
		setupReady := setup.IsReady()
		connectionStatus, connectionAction, showConnection := providerConnectionMenuState(name, data, menu.explicitlyEnabled, setupReady)
		setProviderConnectionItemLocked(menu, connectionAction, showConnection)
		visibleProviderCount++
		if data != nil {
			activeResults[name] = data
		}

		if data == nil {
			showProviderHeader(menu)
			statusTitle := "No data"
			if connectionStatus != "" {
				statusTitle = connectionStatus
			}
			if connectionStatus == "" {
				if !setup.IsReady() && setup.Detail != "" {
					statusTitle = setup.Detail
				}
			}
			setMenuItemTitle(menu.statusItem, &menu.statusState, statusTitle)
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
			hideProviderWindows(menu)
			setMenuItemVisible(menu.dashboardItem, &menu.dashboardState, true)
			continue
		}

		if data.IsExpired {
			if menu.provider.Name() == "claude" {
				hasExpiredClaude = true
			}
			showProviderHeader(menu)
			hideProviderWindows(menu)
			expiredMsg := "Token expired"
			if connectionStatus != "" {
				expiredMsg = connectionStatus
			}
			if data.Error != "" {
				if connectionStatus == "" {
					expiredMsg = format.HumanizeError(data.Error)
				}
			}
			setMenuItemTitle(menu.statusItem, &menu.statusState, expiredMsg)
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
			setMenuItemVisible(menu.dashboardItem, &menu.dashboardState, true)
			continue
		}

		if data.Error != "" {
			showProviderHeader(menu)
			hideProviderWindows(menu)
			setMenuItemTitle(menu.statusItem, &menu.statusState, format.HumanizeError(data.Error))
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
			setMenuItemVisible(menu.dashboardItem, &menu.dashboardState, true)
			continue
		}

		showProviderHeader(menu)
		if data.Stale {
			setMenuItemTitle(menu.statusItem, &menu.statusState, fmt.Sprintf("Usage unavailable - showing last good data from %s", data.FetchedAt.Local().Format("15:04")))
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
		} else if resetTitle := resetCreditTraySummary(data, time.Now()); resetTitle != "" {
			setMenuItemTitle(menu.statusItem, &menu.statusState, resetTitle)
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
		} else {
			setMenuItemVisible(menu.statusItem, &menu.statusState, false)
		}

		windows := data.PresentationWindows()
		for i, window := range windows {
			if i >= len(menu.windowItems) {
				break
			}

			resetStr, indicator := "reset unknown", "reset unknown"
			if !window.ResetsAt.IsZero() {
				proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
				resetStr, indicator = format.FormatDuration(time.Until(window.ResetsAt)), proj.PaceIndicator()
			} else if window.ResetPolicy != "" {
				indicator = window.ResetPolicy
			}
			setMenuItemTitle(menu.windowItems[i], &menu.windowStates[i], fmt.Sprintf("%s: %.0f%% — %s — %s",
				trayWindowLabel(window), window.Utilization, resetStr, indicator))
			setMenuItemVisible(menu.windowItems[i], &menu.windowStates[i], true)

		}

		for i := len(windows); i < len(menu.windowItems); i++ {
			setMenuItemVisible(menu.windowItems[i], &menu.windowStates[i], false)
		}
		for i, balance := range data.Balances {
			if i >= len(menu.balanceItems) {
				break
			}
			label := balance.DisplayName
			if label == "" {
				label = balance.Name
			}
			setMenuItemTitle(menu.balanceItems[i], &menu.balanceStates[i], fmt.Sprintf("%s: %.2f remaining", label, balance.Remaining))
			setMenuItemVisible(menu.balanceItems[i], &menu.balanceStates[i], true)
		}
		for i := len(data.Balances); i < len(menu.balanceItems); i++ {
			setMenuItemVisible(menu.balanceItems[i], &menu.balanceStates[i], false)
		}

		setMenuItemVisible(menu.dashboardItem, &menu.dashboardState, true)
	}

	// Show/hide reauth button (only for Claude expired tokens)
	setSharedMenuItemVisible(mReauth, hasExpiredClaude, &s.reauthVisible, &s.reauthVisibleKnown)

	// Show the "No active providers" placeholder only when no provider is
	// visible. This keeps the tray useful for a fresh install (where it
	// reads as a hint that the user needs to configure something) without
	// adding clutter once at least one provider is showing data.
	if visibleProviderCount == 0 {
		setSharedMenuItemVisible(mEmpty, true, &s.emptyVisible, &s.emptyVisibleKnown)
		setSharedMenuItemVisible(mProviderSetup, true, &s.providerSetupVisible, &s.providerSetupVisibleKnown)
	} else {
		setSharedMenuItemVisible(mEmpty, false, &s.emptyVisible, &s.emptyVisibleKnown)
		setSharedMenuItemVisible(mProviderSetup, false, &s.providerSetupVisible, &s.providerSetupVisibleKnown)
	}

	updateIconTargetSelector(activeResults, displayNames, mIconProvider)

	// Update icon and title based on worst usage (only active providers)
	updateTrayIcon(activeResults)
	updateTrayTitle(activeResults)
	updateTrayTooltip(activeResults, displayNames)

	// Check notification thresholds
	checkThresholds(activeResults, displayNames)
}

func cycleIconSelection(menus map[string]*providerMenuItems, item *systray.MenuItem) {
	displayNames := providerDisplayNames(menus)

	s.mu.Lock()
	results := s.lastResults
	mode := normalizedIconAutoModeLocked()
	choices := manualIconTargets(results, mode)
	s.iconTargetChoices = choices
	if !targetInChoices(s.iconTargetOverride, choices) {
		s.iconTargetOverride = iconTarget{}
	}
	s.iconTargetOverride = nextIconTargetOverride(s.iconTargetOverride, choices, true)
	s.mu.Unlock()

	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()
	updateIconTargetSelector(results, displayNames, item)
	updateTrayIcon(results)
	updateTrayTitle(results)
	updateTrayTooltip(results, displayNames)
}

func resetIconSelection(menus map[string]*providerMenuItems, item *systray.MenuItem, modeItem *systray.MenuItem) {
	displayNames := providerDisplayNames(menus)

	s.mu.Lock()
	results := s.lastResults
	mode := normalizedIconAutoModeLocked()
	choices := manualIconTargets(results, mode)
	s.iconTargetChoices = choices
	s.iconTargetOverride = iconTarget{}
	s.mu.Unlock()

	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()
	updateIconTargetSelector(results, displayNames, item)
	updateIconAutoModeLabel(modeItem)
	updateTrayIcon(results)
	updateTrayTitle(results)
	updateTrayTooltip(results, displayNames)
}

func toggleIconAutoMode(menus map[string]*providerMenuItems, item *systray.MenuItem, modeItem *systray.MenuItem) {
	displayNames := providerDisplayNames(menus)

	s.mu.Lock()
	results := s.lastResults
	current := normalizedIconAutoModeLocked()
	switch current {
	case iconAutoRisk:
		s.iconAutoMode = iconAutoProjected
	case iconAutoProjected:
		s.iconAutoMode = iconAutoRunway
	default:
		s.iconAutoMode = iconAutoRisk
	}
	mode := s.iconAutoMode
	choices := manualIconTargets(results, mode)
	s.iconTargetChoices = choices
	if !targetInChoices(s.iconTargetOverride, choices) {
		s.iconTargetOverride = iconTarget{}
	}
	s.mu.Unlock()

	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()
	updateIconTargetSelector(results, displayNames, item)
	updateIconAutoModeLabel(modeItem)
	updateTrayIcon(results)
	updateTrayTitle(results)
	updateTrayTooltip(results, displayNames)
}

func providerDisplayNames(menus map[string]*providerMenuItems) map[string]string {
	displayNames := make(map[string]string, len(menus))
	for name, menu := range menus {
		displayNames[name] = menu.displayName
	}
	return displayNames
}

func updateIconTargetSelector(results map[string]*provider.UsageData, displayNames map[string]string, item *systray.MenuItem) {
	mode := currentIconAutoMode()
	choices := manualIconTargets(results, mode)

	s.mu.Lock()
	if !targetInChoices(s.iconTargetOverride, choices) {
		s.iconTargetOverride = iconTarget{}
	}
	s.iconTargetChoices = choices
	override := s.iconTargetOverride
	s.mu.Unlock()

	if len(choices) == 0 {
		if item != nil {
			setMenuItemTitle(item, &s.iconTargetState, "Icon: Auto")
			setMenuItemEnabled(item, &s.iconTargetState, false)
		}
		return
	}

	if item != nil {
		setMenuItemEnabled(item, &s.iconTargetState, true)
		setMenuItemTitle(item, &s.iconTargetState, iconCycleMenuTitle(override, displayNames, mode))
	}
}

func updateIconAutoModeLabel(item *systray.MenuItem) {
	if item == nil {
		return
	}
	mode := currentIconAutoMode()
	if mode == iconAutoProjected {
		item.SetTitle("Auto Mode: EST")
		return
	}
	if mode == iconAutoRunway {
		item.SetTitle("Auto Mode: Runway")
		return
	}
	item.SetTitle("Auto Mode: Risk")
}

func iconCycleMenuTitle(target iconTarget, displayNames map[string]string, mode iconAutoMode) string {
	if target.Provider == "" {
		if mode == iconAutoProjected {
			return "Icon: Auto EST (click to cycle)"
		}
		if mode == iconAutoRunway {
			return "Icon: Auto Runway (click to cycle)"
		}
		return "Icon: Auto Risk (click to cycle)"
	}
	return "Icon: " + iconTargetDisplayName(target, displayNames) + " (click for next, double-click for Auto)"
}

func nextIconTargetOverride(current iconTarget, choices []iconTarget, skipAutoCurrent bool) iconTarget {
	if len(choices) == 0 {
		return iconTarget{}
	}
	if current.Provider == "" {
		if skipAutoCurrent && len(choices) > 1 {
			return choices[1]
		}
		return choices[0]
	}
	for i, choice := range choices {
		if choice != current {
			continue
		}
		if i == len(choices)-1 {
			return iconTarget{}
		}
		return choices[i+1]
	}
	return choices[0]
}

func activeIconTargets(results map[string]*provider.UsageData, mode iconAutoMode) []iconTarget {
	choices := activeIconTargetsAllowingStale(results, mode, false)
	if len(choices) > 0 {
		return choices
	}
	return activeIconTargetsAllowingStale(results, mode, true)
}

func manualIconTargets(results map[string]*provider.UsageData, mode iconAutoMode) []iconTarget {
	return activeIconTargetsAllowingStale(results, mode, true)
}

func activeIconTargetsAllowingStale(results map[string]*provider.UsageData, mode iconAutoMode, allowStale bool) []iconTarget {
	if len(results) == 0 {
		return nil
	}
	type rankedTarget struct {
		target        iconTarget
		projection    forecast.Projection
		hasProjection bool
		score         float64
	}

	providerNames := make([]string, 0, len(results))
	for name := range results {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	ranked := make([]rankedTarget, 0, len(results))
	for _, name := range providerNames {
		data := results[name]
		if data == nil {
			continue
		}
		if data.Stale && !allowStale {
			continue
		}
		windows := data.UsableWindows()
		if len(windows) == 0 {
			continue
		}
		for _, window := range windows {
			target := iconTarget{Provider: name, Window: window.Name}
			proj := windowProjection(window)
			score := proj.ProjectedPct
			if mode == iconAutoRunway {
				score = 100 - proj.ProjectedPct
			}
			ranked = append(ranked, rankedTarget{
				target:        target,
				projection:    proj,
				hasProjection: true,
				score:         score,
			})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if mode == iconAutoRisk && ranked[i].hasProjection && ranked[j].hasProjection {
			if cmp := forecast.CompareRisk(ranked[i].projection, ranked[j].projection); cmp != 0 {
				return cmp < 0
			}
		}
		return ranked[i].score > ranked[j].score
	})

	choices := make([]iconTarget, 0, len(ranked))
	for _, choice := range ranked {
		choices = append(choices, choice.target)
	}
	return choices
}

func currentIconAutoMode() iconAutoMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalizedIconAutoModeLocked()
}

func normalizedIconAutoModeLocked() iconAutoMode {
	if s.iconAutoMode == iconAutoProjected {
		return iconAutoProjected
	}
	if s.iconAutoMode == iconAutoRunway {
		return iconAutoRunway
	}
	return iconAutoRisk
}

func iconTargetRisk(data *provider.UsageData, target iconTarget) float64 {
	if data == nil {
		return -1
	}
	if data.IsExpired {
		return 10000
	}
	if data.Error != "" && !data.HasPresentableUsage() {
		return 9000
	}
	if target.Window == "" {
		return providerProjectedPct(data)
	}
	if window, _, ok := selectedIconWindow(data, target.Window); ok {
		return windowProjectedPct(window)
	}
	return -1
}

func windowProjectedPct(window provider.UsageWindow) float64 {
	return windowProjection(window).ProjectedPct
}

func windowProjection(window provider.UsageWindow) forecast.Projection {
	return forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
}

func targetInChoices(target iconTarget, choices []iconTarget) bool {
	if target.Provider == "" {
		return true
	}
	for _, choice := range choices {
		if choice == target {
			return true
		}
	}
	return false
}

func iconTargetDisplayName(target iconTarget, displayNames map[string]string) string {
	display := providerDisplayName(target.Provider, displayNames)
	if target.Window == "" {
		return display
	}
	return display + " " + windowBadgeLabel(target.Window)
}

func providerDisplayName(name string, displayNames map[string]string) string {
	if displayNames != nil && displayNames[name] != "" {
		return displayNames[name]
	}
	if name == "" {
		return "clawmeter"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func selectedTrayTarget(results map[string]*provider.UsageData) (string, *provider.UsageData, string, bool) {
	s.mu.Lock()
	override := s.iconTargetOverride
	mode := normalizedIconAutoModeLocked()
	s.mu.Unlock()
	autoChoices := activeIconTargets(results, mode)

	if override.Provider != "" {
		if targetInChoices(override, manualIconTargets(results, mode)) {
			if data := results[override.Provider]; data != nil {
				return override.Provider, data, override.Window, true
			}
		}
	}

	for _, target := range autoChoices {
		data := results[target.Provider]
		if data == nil {
			continue
		}
		window, _, ok := selectedIconWindow(data, target.Window)
		if ok {
			return target.Provider, data, window.Name, true
		}
	}

	for _, name := range sortedKeys(results) {
		data := results[name]
		if data != nil {
			return name, data, "", true
		}
	}
	return "", nil, "", false
}

func setPendingRelease(rel *update.Release) {
	s.mu.Lock()
	s.pendingRelease = rel
	s.mu.Unlock()
}

func currentPendingRelease() *update.Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingRelease
}

func updateAvailable() bool {
	return currentPendingRelease() != nil
}

func updateTrayIcon(results map[string]*provider.UsageData) {
	// Pick the icon's provider using the same severity ordering as the title
	// and tooltip, but prefer providers with usable quota windows. Setup and
	// fetch errors are actionable in the menu; they should not steal the tray
	// icon from real quota telemetry.
	worstProvider := ""
	meter := icons.MeterState{}

	if _, data, windowName, ok := selectedTrayTarget(results); ok {
		meter = iconMeterState(data, windowName)
		worstProvider = iconProviderName(data)
	}
	meter.UpdateAvailable = updateAvailable()

	s.mu.Lock()
	if s.iconStateKnown && s.currentIconProvider == worstProvider && s.currentIconMeter == meter {
		s.mu.Unlock()
		return
	}
	s.currentIconProvider = worstProvider
	s.currentIconMeter = meter
	s.iconStateKnown = true
	s.mu.Unlock()

	// Each platform renders from its closest native V10 raster. Linux supplies
	// the 22px and 32px forms as SNI pixmaps; Windows and macOS receive the
	// closest source form for their own final compositor scaling.
	setIconDynamic(worstProvider, meter, nil)
}

func iconProviderName(data *provider.UsageData) string {
	if data == nil {
		return ""
	}
	return data.Provider
}

// iconMeterState maps provider usage into the tray icon's three visual
// channels: actual usage, expected usage by this point in the reset window, and
// projected urgency. The popup keeps exact text; the icon carries the comparison.
func iconMeterState(data *provider.UsageData, windowName string) icons.MeterState {
	if data == nil {
		return icons.MeterState{}
	}
	if data.IsExpired {
		return icons.MeterState{UsagePct: 100, ExpectedPct: 100, RiskPct: 100}
	}
	if data.Stale {
		return icons.MeterState{}
	}
	if data.Error != "" && !data.HasPresentableUsage() {
		return icons.MeterState{}
	}

	window, proj, ok := selectedIconWindow(data, windowName)
	if !ok {
		return icons.MeterState{}
	}
	windowLen := forecast.GuessWindowType(window.Name)
	return icons.MeterState{
		UsagePct:     window.Utilization,
		ExpectedPct:  expectedUsagePct(window.ResetsAt, windowLen),
		RiskPct:      proj.ProjectedPct,
		ShowExpected: true,
		Label:        windowBadgeLabelForWindow(window),
	}
}

func expectedUsagePct(resetsAt time.Time, windowLen time.Duration) float64 {
	if windowLen <= 0 {
		return 0
	}
	elapsed := windowLen - time.Until(resetsAt)
	if elapsed <= 0 {
		return 0
	}
	if elapsed >= windowLen {
		return 100
	}
	return elapsed.Seconds() / windowLen.Seconds() * 100
}

func updateTrayTitle(results map[string]*provider.UsageData) {
	title := trayTitle()
	s.mu.Lock()
	changed := title != s.currentTitle
	if changed {
		s.currentTitle = title
	}
	s.mu.Unlock()
	if changed {
		systray.SetTitle(title)
	}
}

func trayTitle() string {
	if updateAvailable() {
		return "Clawmeter •"
	}
	return "Clawmeter"
}

func selectedIconWindow(data *provider.UsageData, windowName string) (provider.UsageWindow, forecast.Projection, bool) {
	var selected provider.UsageWindow
	var selectedProj forecast.Projection
	if data == nil {
		return selected, selectedProj, false
	}
	if windowName != "" {
		for _, window := range data.UsableWindows() {
			if window.Name != windowName {
				continue
			}
			windowLen := forecast.GuessWindowType(window.Name)
			return window, forecast.Project(window.Utilization, window.ResetsAt, windowLen), true
		}
		return selected, selectedProj, false
	}
	hasSelected := false
	for _, window := range data.UsableWindows() {
		proj := windowProjection(window)
		if !hasSelected || forecast.CompareRisk(proj, selectedProj) < 0 {
			selected = window
			selectedProj = proj
			hasSelected = true
		}
	}
	return selected, selectedProj, hasSelected
}

func providerProjectedPct(data *provider.UsageData) float64 {
	_, proj, ok := selectedIconWindow(data, "")
	if !ok {
		return 0
	}
	return proj.ProjectedPct
}

func windowBadgeLabel(name string) string {
	label := strings.ToUpper(strings.TrimSpace(name))
	parts := strings.FieldsFunc(label, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
	if len(parts) > 0 && len(parts[0]) >= 2 && parts[0][0] >= '0' && parts[0][0] <= '9' {
		if len(parts) > 1 && len(parts[1]) > 0 {
			return string([]byte{parts[0][0], parts[1][0]})
		}
		return parts[0][:2]
	}
	var b strings.Builder
	for _, r := range label {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			if b.Len() >= 2 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "--"
	}
	return b.String()
}

func windowBadgeLabelForWindow(window provider.UsageWindow) string {
	if label := strings.TrimSpace(window.DisplayName); label != "" {
		return windowBadgeLabel(label)
	}
	return windowBadgeLabel(window.Name)
}

func updateTrayTooltip(results map[string]*provider.UsageData, displayNames map[string]string) {
	tooltip := trayTooltip(results, displayNames)
	if rel := currentPendingRelease(); rel != nil {
		line := fmt.Sprintf("Update available: %s", rel.Version)
		if strings.TrimSpace(tooltip) == "" {
			tooltip = line
		} else {
			tooltip += "\n" + line
		}
	}
	s.mu.Lock()
	changed := tooltip != s.currentTooltip
	if changed {
		s.currentTooltip = tooltip
	}
	s.mu.Unlock()
	if changed {
		systray.SetTooltip(tooltip)
	}
}

func trayTooltip(results map[string]*provider.UsageData, displayNames map[string]string) string {
	name, data, windowName, ok := selectedTrayTarget(results)
	if !ok || data == nil {
		return ""
	}
	display := providerDisplayName(name, displayNames)
	if data.IsExpired {
		msg := "token expired"
		if data.Error != "" {
			msg = format.HumanizeError(data.Error)
		}
		return fmt.Sprintf("%s: %s", display, msg)
	}
	if data.Error != "" && !data.HasPresentableUsage() {
		return fmt.Sprintf("%s: %s", display, format.HumanizeError(data.Error))
	}
	if data.Stale {
		reason := staleTooltipReason(data)
		if !data.FetchedAt.IsZero() {
			return fmt.Sprintf("%s: stale - showing last good data from %s (%s)", display, data.FetchedAt.Local().Format("15:04"), reason)
		}
		return fmt.Sprintf("%s: stale - showing last good data (%s)", display, reason)
	}

	window, proj, ok := selectedIconWindow(data, windowName)
	if !ok {
		return display
	}
	title := iconTooltipTitle(display, window)
	tooltip := compactIconTooltip(title, window, proj)
	if resetLine := resetCreditTraySummary(data, time.Now()); resetLine != "" {
		tooltip += "\n" + resetLine
	}
	return tooltip
}

func resetCreditTraySummary(data *provider.UsageData, now time.Time) string {
	if data == nil || data.Stale || data.ResetCredits == nil {
		return ""
	}
	count := data.ResetCredits.DisplayCount(now)
	if count <= 0 {
		return ""
	}
	noun := "reset credit"
	if count != 1 {
		noun = "reset credits"
	}
	if expiresAt, ok := data.ResetCredits.EarliestExpiry(now); ok {
		return fmt.Sprintf("%d %s - earliest expires %s", count, noun, expiresAt.Local().Format("Jan 2 3:04 PM"))
	}
	return fmt.Sprintf("%d %s available", count, noun)
}

func staleTooltipReason(data *provider.UsageData) string {
	if data == nil || data.Warning == "" {
		return "usage unavailable"
	}
	return format.HumanizeError(data.Warning)
}

func compactIconTooltip(title string, window provider.UsageWindow, proj forecast.Projection) string {
	parts := make([]string, 0, 4)
	if title != "" {
		parts = append(parts, title)
	}
	switch {
	case proj.RunOutNote() != "":
		parts = append(parts, upperFirst(proj.RunOutNote()))
	case !proj.WillLastToReset:
		parts = append(parts, "Out now")
	default:
		parts = append(parts, "Won't run out")
	}
	parts = append(parts, "Resets in "+format.FormatDuration(time.Until(window.ResetsAt)))
	parts = append(parts, compactProjectionEstimate(proj))
	return strings.Join(parts, "\n")
}

func upperFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func iconTooltipTitle(display string, window provider.UsageWindow) string {
	windowLabel := humanWindowLabel(window)
	if windowLabel == "" {
		return display
	}
	if display == "" {
		return windowLabel
	}
	return display + " " + windowLabel
}

func humanWindowLabel(window provider.UsageWindow) string {
	switch strings.TrimSpace(window.Name) {
	case "5h":
		return "5-Hour"
	case "7d":
		return "7-Day"
	case "7d All":
		return "7-Day All Models"
	case "7d OAuth":
		return "7-Day OAuth Apps"
	case "7d Opus":
		return "7-Day Opus"
	case "7d Sonnet":
		return "7-Day Sonnet"
	}

	label := strings.TrimSpace(window.DisplayName)
	if label == "" {
		label = strings.TrimSpace(window.Name)
	}
	switch strings.ToLower(label) {
	case "5 hours":
		return "5-Hour"
	case "7 days":
		return "7-Day"
	case "7 days (all models)":
		return "7-Day All Models"
	case "7 days (oauth apps)":
		return "7-Day OAuth Apps"
	case "7 days (opus)":
		return "7-Day Opus"
	case "7 days (sonnet)":
		return "7-Day Sonnet"
	}
	return label
}

// trayWindowLabel preserves provider-supplied compact labels such as "5h" and
// "7d" for the constrained tray menu, falling back to the stable internal name.
func trayWindowLabel(window provider.UsageWindow) string {
	if label := strings.TrimSpace(window.DisplayName); label != "" {
		return label
	}
	return strings.TrimSpace(window.Name)
}

func compactProjectionEstimate(proj forecast.Projection) string {
	estimate := forecast.PaceLabel(proj.ProjectedPct)
	if strings.HasPrefix(estimate, "est.") {
		return "Est." + strings.TrimPrefix(estimate, "est.")
	}
	return estimate
}

func checkThresholds(results map[string]*provider.UsageData, displayNames map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastResults == nil {
		s.lastResults = make(map[string]*provider.UsageData)
	}

	warningThreshold := 80.0
	criticalThreshold := 95.0
	if cfg != nil {
		warningThreshold = cfg.Settings.NotificationThresholds.Warning
		criticalThreshold = cfg.Settings.NotificationThresholds.Critical
	}

	for name, data := range results {
		if data == nil || data.Error != "" || data.Stale {
			continue
		}

		oldData, hadOld := s.lastResults[name]

		for _, window := range data.UsableWindows() {
			pct := window.Utilization
			oldPct := 0.0

			if hadOld && oldData != nil {
				if oldWindow, ok := oldData.GetWindow(window.Name); ok {
					oldPct = oldWindow.Utilization
				}
			}

			display := displayNames[name]
			if display == "" {
				display = name
			}
			proj := forecast.Project(pct, window.ResetsAt, forecast.GuessWindowType(window.Name))
			message := fmt.Sprintf("%s window at %.0f%% — %s", window.Name, pct, proj.PaceIndicator())

			if pct >= criticalThreshold && oldPct < criticalThreshold {
				notify(fmt.Sprintf("%s usage critical", display), message, "critical")
			} else if pct >= warningThreshold && oldPct < warningThreshold {
				notify(fmt.Sprintf("%s usage warning", display), message, "normal")
			}
		}
	}

	// Update stored results
	s.lastResults = results
}

func setErrorState(msg string) {
	setIconByName("gray", icons.Gray)
	systray.SetTitle("Clawmeter")
	systray.SetTooltip("")
}

func updateAutostartLabel(m *systray.MenuItem) {
	if !autostart.IsSupported() {
		m.SetTitle("Launch at login (unsupported on this OS)")
		m.Disable()
		return
	}
	if autostart.IsInstalled() {
		m.SetTitle("✓ Launch at login")
	} else {
		m.SetTitle("  Launch at login")
	}
}

func toggleAutostart(m *systray.MenuItem) {
	if !autostart.IsSupported() {
		trayRenderMu.Lock()
		updateAutostartLabel(m)
		trayRenderMu.Unlock()
		return
	}
	if autostart.IsInstalled() {
		if err := autostart.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: failed to disable launch at login: %v\n", err)
			notify("Clawmeter", fmt.Sprintf("Could not disable launch at login: %v", err), "normal")
		}
	} else {
		if err := autostart.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: failed to enable launch at login: %v\n", err)
			notify("Clawmeter", fmt.Sprintf("Could not enable launch at login: %v", err), "normal")
		}
	}
	trayRenderMu.Lock()
	updateAutostartLabel(m)
	trayRenderMu.Unlock()
}

func openURL(url string) {
	_ = browser.OpenURL(url)
}

// notify delivers a desktop notification using beeep, which natively wraps
// each platform's notification system (Windows toast, macOS Notification
// Center, Linux D-Bus org.freedesktop.Notifications). The "critical"
// urgency maps to beeep.Alert, which renders with the highest-priority
// visual treatment each platform offers; everything else uses beeep.Notify.
func notify(title, body, urgency string) {
	if urgency == "critical" {
		_ = beeep.Alert(title, body, "")
		return
	}
	_ = beeep.Notify(title, body, "")
}

func providerNames(providers []provider.Provider) []string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	return names
}

func providerSourceRevisions(providers []provider.Provider) map[string]string {
	revisions := make(map[string]string)
	for _, p := range providers {
		if revision := provider.SourceRevision(p); revision != "" {
			revisions[provider.SourceKey(p)] = revision
		}
	}
	return revisions
}

func cloneSourceRevisions(revisions map[string]string) map[string]string {
	cloned := make(map[string]string, len(revisions))
	for name, revision := range revisions {
		cloned[name] = revision
	}
	return cloned
}

func resultsMatchingSourceRevisions(results map[string]*provider.UsageData, previous, current map[string]string) map[string]*provider.UsageData {
	matched := make(map[string]*provider.UsageData, len(results))
	for name, data := range results {
		if cache.SourceRevisionMatches(previous, name, current[name]) {
			matched[name] = data
		}
	}
	return matched
}

func cachedResultsForCurrentSources(entry *cache.Entry, providers []provider.Provider) map[string]*provider.UsageData {
	current := providerSourceRevisions(providers)
	results := make(map[string]*provider.UsageData)
	for _, p := range providers {
		name := provider.SourceKey(p)
		if !cache.SourceRevisionMatches(entry.SourceRevisions, name, current[name]) {
			continue
		}
		if data, ok := entry.ProviderData[name]; ok {
			results[name] = data
		}
	}
	return results
}

func splitProvidersForRefresh(providers []provider.Provider, gate *provider.FailureGate, lastResults map[string]*provider.UsageData, force bool) ([]provider.Provider, map[string]*provider.UsageData) {
	toFetch := make([]provider.Provider, 0, len(providers))
	skipped := make(map[string]*provider.UsageData)
	for _, p := range providers {
		name := provider.SourceKey(p)
		prev := lastResults[name]
		if !force && gate != nil && gate.InBackoff(name) && prev != nil && prev.EstablishesPrimaryUIHistory() {
			skipped[name] = prev.Clone()
			continue
		}
		toFetch = append(toFetch, p)
	}
	return toFetch, skipped
}

// sortedKeys returns provider names sorted by severity (worst first).
// Expired > error > risk-window urgency > alphabetical.
func sortedKeys(m map[string]*provider.UsageData) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		di, dj := m[keys[i]], m[keys[j]]
		si, sj := providerSeverity(di), providerSeverity(dj)
		if si.tier != sj.tier {
			return si.tier < sj.tier
		}
		if si.hasProjection && sj.hasProjection {
			if cmp := forecast.CompareRisk(si.projection, sj.projection); cmp != 0 {
				return cmp < 0
			}
		}
		return keys[i] < keys[j] // alphabetical tiebreak
	})
	return keys
}

type providerSeverityRank struct {
	tier          int
	projection    forecast.Projection
	hasProjection bool
}

// providerSeverity returns a sortable severity rank.
func providerSeverity(data *provider.UsageData) providerSeverityRank {
	if data == nil {
		return providerSeverityRank{tier: 3}
	}
	if data.IsExpired {
		return providerSeverityRank{tier: 0}
	}
	if data.Error != "" && !data.HasPresentableUsage() {
		return providerSeverityRank{tier: 1}
	}
	_, proj, ok := selectedIconWindow(data, "")
	return providerSeverityRank{tier: 2, projection: proj, hasProjection: ok}
}
