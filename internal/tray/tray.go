//go:build tray

package tray

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/beeep"
	"github.com/pkg/browser"
	"github.com/tnunamak/clawmeter/internal/systray"

	"github.com/tnunamak/clawmeter/internal/autostart"
	"github.com/tnunamak/clawmeter/internal/browserext"
	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	claudewebprovider "github.com/tnunamak/clawmeter/internal/provider/claudeweb"
	"github.com/tnunamak/clawmeter/internal/shellpath"
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
	"github.com/tnunamak/clawmeter/internal/update"
)

const (
	updateCheckInterval    = 30 * time.Minute
	tokenPlanProviderName  = "alibaba_token"
	retryConnectionAction  = "Retry quota access"
	tokenPlanConnectTarget = "token-plan"
)

func resetPollIntervalIfChanged(currentSeconds, nextSeconds int, reset func(time.Duration)) int {
	if currentSeconds == nextSeconds {
		return currentSeconds
	}
	reset(config.PollInterval(nextSeconds))
	return nextSeconds
}

func claudeWebProviderKey(target string) string {
	if target == "claude" {
		return "claude_web"
	}
	return "claude_web:" + strings.TrimPrefix(target, "claude:")
}

func trayProviderDisplayName(displayName, label string, repeated bool) string {
	if repeated && label != "" {
		return displayName + " · " + label
	}
	return displayName
}

func shouldCreateProviderMenu(p provider.Provider) bool {
	return p.Name() != "claude_web"
}

type state struct {
	mu                        sync.Mutex
	lastResults               map[string]*provider.UsageData
	thresholdUtilization      map[iconTarget]thresholdSample
	lastSourceRevisions       map[string]string
	statuses                  map[string]*status.ProviderStatus
	failureGate               *provider.FailureGate
	pendingRelease            *update.Release
	currentTitle              string
	currentTooltip            string
	lastRefreshAt             time.Time
	iconTargetOverride        iconTarget
	iconStateKnown            bool
	currentIconProvider       string
	currentIconMeter          icons.MeterState
	reauthVisible             bool
	reauthVisibleKnown        bool
	extensionVisible          bool
	extensionVisibleKnown     bool
	emptyVisible              bool
	emptyVisibleKnown         bool
	providerSetupVisible      bool
	providerSetupVisibleKnown bool
}

type iconTarget struct {
	Provider string
	Window   string
}

type thresholdSample struct {
	Utilization float64
	ResetsAt    time.Time
}

var (
	s       state
	version string
	cfg     *config.Config
	// systray's Linux implementation performs a synchronous D-Bus refresh for
	// every menu mutation. Keep background refreshes and click handlers from
	// mutating the native menu concurrently.
	trayRenderMu              sync.Mutex
	apiCreditsTimer           *time.Timer
	apiCreditsTimerGeneration uint64
)

func Run(ver string) int {
	redirectLogToFile()
	configureNotificationIdentity()
	version = ver
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
	loaded, err := config.Load(all.SourceValidator())
	if err != nil {
		setErrorState("Config error")
		systray.BeginMenuUpdate()
		mError := systray.AddMenuItem("Config error — fix config.yaml; retrying automatically", "")
		mError.Disable()
		mQuit := systray.AddMenuItem("Quit", "")
		systray.EndMenuUpdate()
		go func() {
			ticker := time.NewTicker(config.PollInterval(0))
			defer ticker.Stop()
			retryTrayConfig(ticker.C, mQuit.ClickedCh, func() (*config.Config, error) {
				return config.Load(all.SourceValidator())
			}, func(loaded *config.Config) {
				mError.Hide()
				mQuit.Hide()
				onReadyWithConfig(loaded)
			}, systray.Quit)
		}()
		return
	}
	onReadyWithConfig(loaded)
}

func retryTrayConfig(ticks <-chan time.Time, quit <-chan struct{}, load func() (*config.Config, error), ready func(*config.Config), stop func()) {
	for {
		select {
		case <-quit:
			stop()
			return
		case <-ticks:
			loaded, err := load()
			// Quit may arrive while the synchronous config load is in flight.
			select {
			case <-quit:
				stop()
				return
			default:
			}
			if err == nil {
				ready(loaded)
				return
			}
		}
	}
}

func onReadyWithConfig(loaded *config.Config) {
	s.mu.Lock()
	cfg = loaded
	s.mu.Unlock()
	s.failureGate = provider.NewFailureGate()

	// Build the initial native menu as one transaction. On Linux, each menu
	// mutation otherwise emits a separate D-Bus layout signal while Plasma is
	// trying to register the tray item.
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()

	// Launch at login is opt-in: the user enables it from the tray menu.
	// Do not silently persist anything on first run.

	setIconByName("gray", icons.Gray)
	systray.SetTitle(trayTitleForPlatform(runtime.GOOS, false))
	systray.SetTooltip("")

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
	// Providers appear alphabetically by the name the menu shows.
	providers := registry.GetAll()
	sort.SliceStable(providers, func(i, j int) bool {
		return strings.ToLower(providerMenuName(providers[i], familyCounts[providers[i].Name()] > 1)) <
			strings.ToLower(providerMenuName(providers[j], familyCounts[providers[j].Name()] > 1))
	})
	for _, p := range providers {
		if !shouldCreateProviderMenu(p) {
			continue
		}
		explicit := cfg.IsProviderExplicitlyEnabled(provider.FamilyName(p))
		menu := createProviderMenuItems(p, explicit, providerConnectActions, familyCounts[p.Name()] > 1)
		menu.order = len(providerMenus)
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

	// Hint shown only when Claude's OAuth token has expired. We don't try
	// to launch a terminal for the user — Windows has no obvious "default
	// terminal" we can rely on, and the macOS/Linux versions of that flow
	// saved at most one keystroke. A disabled menu item is the same
	// outcome with zero platform-specific code.
	mReauth := systray.AddMenuItem("Claude login expired — run `claude` to renew it", "")
	mReauth.Disable()
	mReauth.Hide()
	s.reauthVisible = false
	s.reauthVisibleKnown = true

	// Offer the browser extension until it is set up. A copy installed
	// earlier is refreshed to this version.
	mExtension = systray.AddMenuItem(extensionMenuTitle, "Install the extension that reads Claude resets and API credits")
	mExtension.Hide()
	if extensionFolderExists() {
		go func() {
			if _, err := browserext.Install(); err != nil {
				log.Printf("refresh browser extension: %v", err)
			}
		}()
	}

	var updateChecking sync.Mutex
	mUpdate := systray.AddMenuItem("", "")
	mUpdate.Hide()

	mRefresh := systray.AddMenuItem("Refresh Now", "")
	mSettings := systray.AddMenuItem("Settings", "")
	iconPick := newIconPicker(mSettings)
	mAutostart := mSettings.AddSubMenuItemCheckbox("Launch at login", "", false)
	updateAutostartLabel(mAutostart)
	mSetupExtension := mSettings.AddSubMenuItem("Set up the browser extension…", "")
	mReport := mSettings.AddSubMenuItem("Report a problem…", "")
	mSettings.AddSubMenuItem(fmt.Sprintf("Clawmeter %s", version), "").Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "")

	// Guard against concurrent refreshes
	var refreshing sync.Mutex

	ticker := time.NewTicker(config.PollInterval(loaded.Settings.PollInterval))
	pollIntervalSeconds := loaded.Settings.PollInterval
	windowExhaustion.load(exhaustionPath(), 2*config.PollInterval(pollIntervalSeconds))

	// reloadConfig re-reads config.yaml from disk and propagates changes
	// to the registry filter and per-provider menu state. This is what
	// makes `clawmeter config enable <provider>` (or any other out-of-band
	// edit) reflect in the running tray without a restart.
	reloadConfig := func() {
		newCfg, err := config.Load(all.SourceValidator())
		if err != nil {
			return
		}
		s.mu.Lock()
		cfg = newCfg
		s.mu.Unlock()
		registry.SetEnabledFilter(newCfg)
		applyProviderEnablement(providerMenus, newCfg)
		pollIntervalSeconds = resetPollIntervalIfChanged(pollIntervalSeconds, newCfg.Settings.PollInterval, ticker.Reset)
	}

	// Refresh usage only (lightweight, runs every poll cycle). It reports
	// false when another refresh was already running.
	refresh := func(force bool) bool {
		if !refreshing.TryLock() {
			return false
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

		windowExhaustion.observeFetched(result.Results, skipped, sourceIdentities(configured, result.SourceRevisions), time.Now(), 2*config.PollInterval(pollIntervalSeconds))
		_ = cache.Write(result)

		now := time.Now()
		s.mu.Lock()
		s.lastResults = result.Results
		s.lastSourceRevisions = result.SourceRevisions
		s.lastRefreshAt = now
		statuses := s.statuses // reuse last known statuses
		s.mu.Unlock()

		updateUI(result.Results, statuses, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
		trayRenderMu.Lock()
		mRefresh.SetTitle(fmt.Sprintf("Refresh Now  (updated %s)", now.Format("15:04")))
		trayRenderMu.Unlock()
		return true
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
			updateUI(lastResults, statuses, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
		}
	}

	// Check update function
	checkUpdate := func() {
		s.mu.Lock()
		currentCfg := cfg
		s.mu.Unlock()
		if !currentCfg.ShouldCheckForUpdates() {
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
		results := provider.PresentResetCreditSources(s.lastResults)
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
		updateUI(filtered, nil, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
	}

	extensionCtx, stopExtension := context.WithCancel(context.Background())
	publishAPICredits := func() {
		publishLocalResetSource(&refreshing, registry, "claude_api", providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
	}
	go func() {
		err := claudeweb.ServeExtension(extensionCtx, func() []claudeweb.Account {
			return claudewebprovider.Accounts(registry.GetFamily("claude"))
		}, func(result claudeweb.Result) {
			if result.IsResets() {
				publishLocalResetSource(&refreshing, registry, claudeWebProviderKey(result.Resets.ResetCreditsTarget), providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
				return
			}
			publishAPICredits()
		}, browserext.Token)
		if err != nil {
			log.Printf("claude extension listener: %v", err)
		}
	}()

	// Initial refresh in background (don't block tray UI)
	go refresh(false)
	go refreshStatus()

	// Check for updates on startup
	go checkUpdate()

	// Setup tickers
	statusTicker := time.NewTicker(15 * time.Minute) // status pages checked less often
	updateTicker := time.NewTicker(updateCheckInterval)
	loginTicker := time.NewTicker(loginCheckInterval)
	go func() {
		var watch loginWatch
		for range loginTicker.C {
			shellpath.Init()
			now := time.Now()
			current := loginRevisions(registry.GetConfigured())
			s.mu.Lock()
			refreshed := cloneSourceRevisions(s.lastSourceRevisions)
			s.mu.Unlock()
			if watch.due(current, refreshed, now) && refresh(false) {
				watch.ran(now)
			}
		}
	}()
	// Event loop (never blocks on network I/O)
	go func() {
		for {
			select {
			case <-ticker.C:
				// Let the remote refresh start before queuing a local publication.
				go func() {
					refresh(false)
					publishAPICredits()
				}()
			case <-statusTicker.C:
				go refreshStatus()
			case <-updateTicker.C:
				go checkUpdate()
			case <-mRefresh.ClickedCh:
				go refresh(true)
				go checkUpdate()
			case target := <-iconPick.picked:
				selectIconTarget(providerMenus, iconPick, target)
			case providerName := <-providerConnectActions:
				menu := providerMenus[providerName]
				if menu == nil || !menu.connectable {
					continue
				}
				if !menu.connecting.CompareAndSwap(false, true) {
					continue
				}
				trayRenderMu.Lock()
				setProviderConnectionItemLocked(menu, "", true)
				trayRenderMu.Unlock()
				go func(providerName string, menu *providerMenuItems) {
					err := connectProviderFromTray(providerName)
					finishProviderConnection(menu, err)
					if err != nil {
						notify(menu.provider.DisplayName(), err.Error(), "normal")
						return
					}
					notify(menu.provider.DisplayName(), "Quota access connected. Refreshing...", "low")
					go refresh(true)
					go refreshStatus()
				}(providerName, menu)
			case <-mExtension.ClickedCh:
				go openExtensionSetup()
			case <-mSetupExtension.ClickedCh:
				go openExtensionSetup()
			case <-mReport.ClickedCh:
				go openURL(issueURL(version, runtime.GOOS+"/"+runtime.GOARCH, os.Getenv("XDG_CURRENT_DESKTOP")))
			case <-mUpdate.ClickedCh:
				// Download, verify, replace, and restart can take seconds. Keep
				// the tray event loop available while the update runs.
				go applyUpdate()
			case <-mAutostart.ClickedCh:
				go toggleAutostart(mAutostart)
			case <-mQuit.ClickedCh:
				stopExtension()
				trayRenderMu.Lock()
				apiCreditsTimerGeneration++
				if apiCreditsTimer != nil {
					apiCreditsTimer.Stop()
				}
				trayRenderMu.Unlock()
				ticker.Stop()
				statusTicker.Stop()
				updateTicker.Stop()
				loginTicker.Stop()
				systray.Quit()
				return
			}
		}
	}()
}

func applyProviderEnablement(menus map[string]*providerMenuItems, cfg *config.Config) {
	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	for _, menu := range menus {
		menu.explicitlyEnabled = cfg.IsProviderExplicitlyEnabled(provider.FamilyName(menu.provider))
	}
}

// providerMenuItems holds menu items for a single provider.
type providerMenuItems struct {
	provider          provider.Provider
	displayName       string
	headerItem        *systray.MenuItem
	statusItem        *systray.MenuItem
	resetItem         *systray.MenuItem
	connectable       bool // the name row can run the quota connection
	headerAction      string
	headerConnects    atomic.Bool
	windowItems       []*systray.MenuItem
	balanceItems      []*systray.MenuItem
	separator         *systray.MenuItem
	order             int // position in the menu, top first
	opensDashboard    bool
	everHealthy       bool // true once we've seen useful quota data
	explicitlyEnabled bool
	connecting        atomic.Bool
	connectFailed     atomic.Bool // last tray connection attempt failed
	headerState       menuItemState
	statusState       menuItemState
	resetState        menuItemState
	windowStates      []menuItemState
	windowAtRisk      []bool
	balanceStates     []menuItemState
	balanceActions    []string // per balance row; guarded by trayRenderMu
	separatorState    menuItemState
}

type menuItemState struct {
	title       string
	visible     bool
	enabled     bool
	initialized bool
}

const maxWindowItems = 8 // pre-allocate up to 8 window slots per provider

// providerMenuName is the provider's header in the menu, with the account
// added when the provider has more than one.
func providerMenuName(p provider.Provider, repeated bool) string {
	label := provider.SourceLabel(p)
	if label == "" {
		label = provider.SourceID(p)
	}
	return trayProviderDisplayName(p.DisplayName(), label, repeated)
}

func createProviderMenuItems(p provider.Provider, explicitlyEnabled bool, connectActions chan<- string, repeated bool) *providerMenuItems {
	displayName := providerMenuName(p, repeated)
	// A line above each provider block, hidden with the block.
	separator := systray.AddSeparatorItem()
	// The header names the provider and opens its dashboard when clicked.
	opensDashboard := p.Name() != "claude_web" && p.DashboardURL() != ""
	connectable := p.Name() == tokenPlanProviderName && connectActions != nil
	header := systray.AddMenuItem(displayName, p.DashboardURL())
	setMenuItemIcon(header, icons.MenuMark(p.Name(), resolveTrayPalette()))
	if !opensDashboard && !connectable {
		header.Disable()
	}

	// Status item (shows loading/error)
	statusItem := systray.AddMenuItem("Loading...", "")
	statusItem.Disable()
	setMenuItemIcon(statusItem, icons.MenuBlank())

	// Pre-create window items (hidden until populated)
	windowItems := make([]*systray.MenuItem, maxWindowItems)
	for i := range windowItems {
		item := systray.AddMenuItem("", "")
		item.Disable()
		item.Hide()
		setMenuItemIcon(item, icons.MenuBlank())
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
		setMenuItemIcon(item, icons.MenuBlank())
		balanceItems[i] = item
	}
	balanceStates := make([]menuItemState, len(balanceItems))
	for i := range balanceStates {
		balanceStates[i] = menuItemState{visible: false, enabled: false, initialized: true}
	}

	// Quota rows come first, then money, then reset credits.
	resetItem := systray.AddMenuItem("", "")
	resetItem.Disable()
	setMenuItemIcon(resetItem, icons.MenuBlank())
	resetItem.Hide()
	resetState := menuItemState{visible: false, enabled: false, initialized: true}

	menu := &providerMenuItems{
		provider:          p,
		displayName:       displayName,
		headerItem:        header,
		statusItem:        statusItem,
		resetItem:         resetItem,
		connectable:       connectable,
		windowItems:       windowItems,
		balanceItems:      balanceItems,
		separator:         separator,
		opensDashboard:    opensDashboard,
		explicitlyEnabled: explicitlyEnabled,
		headerState:       menuItemState{title: displayName, visible: true, enabled: opensDashboard || connectable, initialized: true},
		statusState:       menuItemState{title: "Loading...", visible: true, enabled: false, initialized: true},
		resetState:        resetState,
		windowStates:      windowStates,
		windowAtRisk:      make([]bool, len(windowItems)),
		balanceStates:     balanceStates,
		balanceActions:    make([]string, len(balanceItems)),
		separatorState:    menuItemState{visible: true, initialized: true},
	}
	for row, item := range balanceItems {
		go func() {
			for range item.ClickedCh {
				openBalanceRow(menu, row, openURL)
			}
		}()
	}
	go func() {
		for range header.ClickedCh {
			if menu.headerConnects.Load() {
				select {
				case connectActions <- p.Name():
				default:
				}
			} else if opensDashboard {
				openURL(p.DashboardURL())
			}
		}
	}()
	return menu
}

// openBalanceRow opens the Console for a balance row that carries an action.
func openBalanceRow(menu *providerMenuItems, row int, open func(string)) {
	trayRenderMu.Lock()
	action := menu.balanceActions[row]
	trayRenderMu.Unlock()
	if action != "" {
		open(claudeweb.ConsoleURL)
	}
}

func setProviderSeparatorVisible(menu *providerMenuItems, visible bool) {
	setMenuItemVisible(menu.separator, &menu.separatorState, visible)
}

func hideProviderMenu(menu *providerMenuItems) {
	setMenuItemVisible(menu.headerItem, &menu.headerState, false)
	setMenuItemVisible(menu.statusItem, &menu.statusState, false)
	setMenuItemVisible(menu.resetItem, &menu.resetState, false)
	hideProviderWindows(menu)
	setProviderSeparatorVisible(menu, false)
}

// showBalanceRows fills the balance rows with balances, then API credit
// pools, and hides the rest. Pools are browser-checked snapshots, so they
// stay visible even when the row's usage reading failed.
func showBalanceRows(menu *providerMenuItems, data *provider.UsageData, balances []provider.UsageBalance) int {
	rows := 0
	show := func(title, action string) {
		if rows < len(menu.balanceItems) {
			setMenuItemTitle(menu.balanceItems[rows], &menu.balanceStates[rows], title)
			setMenuItemVisible(menu.balanceItems[rows], &menu.balanceStates[rows], true)
			setMenuItemEnabled(menu.balanceItems[rows], &menu.balanceStates[rows], action != "")
			menu.balanceActions[rows] = action
			rows++
		}
	}
	for _, balance := range balances {
		label := balance.DisplayName
		if label == "" {
			label = balance.Name
		}
		show(fmt.Sprintf("%s: %.2f remaining", label, balance.Remaining), "")
	}
	for _, pool := range data.CreditPools {
		title := pool.TrayLine(time.Now())
		// A lone pool on the Claude API row needs no label; anywhere else it
		// sits among other rows.
		if data.Provider != provider.ClaudeAPISourceName || len(data.CreditPools) > 1 {
			title = pool.Name + ": " + title
		}
		show(title, pool.Action)
	}
	for i := rows; i < len(menu.balanceItems); i++ {
		setMenuItemVisible(menu.balanceItems[i], &menu.balanceStates[i], false)
		setMenuItemEnabled(menu.balanceItems[i], &menu.balanceStates[i], false)
		menu.balanceActions[i] = ""
	}
	return rows
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
	title := menu.displayName
	if menu.headerAction != "" {
		title += " · " + menu.headerAction
	}
	setMenuItemTitle(menu.headerItem, &menu.headerState, title)
	setMenuItemVisible(menu.headerItem, &menu.headerState, true)
}

// showProviderProblem puts a provider's only message on its name row, so a
// provider without readings takes one line.
func showProviderProblem(menu *providerMenuItems, problem string) {
	if menu.headerAction != "" {
		// The action names the fix; it replaces the problem.
		problem = menu.headerAction
	}
	setMenuItemTitle(menu.headerItem, &menu.headerState, menu.displayName+" · "+problem)
	setMenuItemVisible(menu.headerItem, &menu.headerState, true)
	setMenuItemVisible(menu.statusItem, &menu.statusState, false)
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
	if !menu.connectable {
		return
	}
	if menu.connecting.Load() {
		menu.headerAction = "Connecting..."
		menu.headerConnects.Store(false)
		setMenuItemTitle(menu.headerItem, &menu.headerState, menu.displayName+" · "+menu.headerAction)
		setMenuItemEnabled(menu.headerItem, &menu.headerState, false)
		return
	}
	menu.headerAction = ""
	if show {
		menu.headerAction = action
		setMenuItemTitle(menu.headerItem, &menu.headerState, menu.displayName+" · "+action)
	}
	menu.headerConnects.Store(show)
	setMenuItemEnabled(menu.headerItem, &menu.headerState, true)
}

func finishProviderConnection(menu *providerMenuItems, err error) {
	menu.connectFailed.Store(err != nil)
	menu.connecting.Store(false)
	if err == nil {
		return
	}
	trayRenderMu.Lock()
	setProviderConnectionItemLocked(menu, retryConnectionAction, true)
	trayRenderMu.Unlock()
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

func updateUI(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus, menus map[string]*providerMenuItems, mReauth *systray.MenuItem, iconPick *iconPicker, mEmpty *systray.MenuItem, mProviderSetup *systray.MenuItem) {
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

	results, statuses = lockLatestTrayState(&trayRenderMu)
	defer trayRenderMu.Unlock()
	fetched := results
	results = provider.PresentResetCreditSources(results)
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()

	hasExpiredClaude := false
	hasClaude := false
	displayNames := make(map[string]string, len(menus))
	activeResults := make(map[string]*provider.UsageData, len(results))
	visibleProviderCount := 0
	windowRows := trayMenuRows(results, menus)

	for name, menu := range menus {
		displayNames[name] = menu.displayName
		data := results[name]
		if data == nil && fetched[name] != nil {
			// Folded under another row, like linked API credit pools.
			hideProviderMenu(menu)
			continue
		}

		if data != nil && data.EstablishesPrimaryUIHistory() {
			menu.everHealthy = true
		}

		setup := setups[name]
		// Like the CLI, an auto-detected provider shows only with something
		// to show. The Token Plan also shows when it can be connected.
		detected := name == tokenPlanProviderName && setup.State != provider.SetupUnavailable
		if !provider.ShouldShowInPrimaryUI(data, menu.everHealthy, menu.explicitlyEnabled) && !detected {
			hideProviderMenu(menu)
			continue
		}
		updateResetCreditRow(menu, data, time.Now())
		if provider.FamilyName(menu.provider) == "claude" {
			hasClaude = true
		}
		setupReady := setup.IsReady()
		connectionStatus, connectionAction, showConnection := providerConnectionMenuState(name, data, menu.explicitlyEnabled, setupReady)
		if !showConnection {
			menu.connectFailed.Store(false)
		} else if menu.connectFailed.Load() {
			connectionAction = retryConnectionAction
		}
		setProviderConnectionItemLocked(menu, connectionAction, showConnection)
		visibleProviderCount++
		if data != nil {
			activeResults[name] = data
		}

		if data == nil {
			statusTitle := "No data"
			if connectionStatus != "" {
				statusTitle = connectionStatus
			}
			if connectionStatus == "" {
				if !setup.IsReady() && setup.Detail != "" {
					statusTitle = setup.Detail
				}
			}
			showProviderProblem(menu, statusTitle)
			hideProviderWindows(menu)
			setProviderSeparatorVisible(menu, true)
			continue
		}

		if data.IsExpired {
			if menu.provider.Name() == "claude" {
				hasExpiredClaude = true
			}
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
			showProviderProblem(menu, expiredMsg)
			showBalanceRows(menu, data, nil)
			setProviderSeparatorVisible(menu, true)
			continue
		}

		if data.Error != "" {
			hideProviderWindows(menu)
			showProviderProblem(menu, format.HumanizeError(data.Error))
			showBalanceRows(menu, data, nil)
			setProviderSeparatorVisible(menu, true)
			continue
		}

		showProviderHeader(menu)
		var statusParts []string
		if providerStatus := statuses[provider.FamilyName(menu.provider)]; providerStatus != nil && providerStatus.Indicator.HasIssue() {
			statusParts = append(statusParts, providerStatus.Indicator.Label())
		}
		if data.Stale {
			statusParts = append(statusParts, fmt.Sprintf("Usage unavailable - showing last good data from %s", data.FetchedAt.Local().Format("15:04")))
		}
		if len(statusParts) > 0 {
			setMenuItemTitle(menu.statusItem, &menu.statusState, strings.Join(statusParts, " - "))
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
		} else {
			setMenuItemVisible(menu.statusItem, &menu.statusState, false)
		}

		windows := meter.Shown(data.PresentationWindows())
		rows := windowRows[name]
		for i, window := range windows {
			if i >= len(menu.windowItems) {
				break
			}

			setMenuItemTitle(menu.windowItems[i], &menu.windowStates[i], rows[i])
			if atRisk := windowRunsOutBeforeReset(window); atRisk != menu.windowAtRisk[i] {
				menu.windowAtRisk[i] = atRisk
				icon := icons.MenuBlank()
				if atRisk {
					icon = icons.MenuRiskDot()
				}
				setMenuItemIcon(menu.windowItems[i], icon)
			}
			setMenuItemVisible(menu.windowItems[i], &menu.windowStates[i], true)

		}

		for i := len(windows); i < len(menu.windowItems); i++ {
			setMenuItemVisible(menu.windowItems[i], &menu.windowStates[i], false)
		}
		balanceRows := showBalanceRows(menu, data, data.Balances)
		if len(windows) == 0 && balanceRows == 0 && len(statusParts) == 0 && !menu.resetState.visible {
			showProviderProblem(menu, "no usage reported")
		}
		setProviderSeparatorVisible(menu, true)
	}

	// A line separates providers; none sits above the first one.
	var first *providerMenuItems
	for _, menu := range menus {
		if menu.headerState.visible && (first == nil || menu.order < first.order) {
			first = menu
		}
	}
	for _, menu := range menus {
		if menu.headerState.visible {
			setProviderSeparatorVisible(menu, menu != first)
		}
	}

	// Show/hide reauth button (only for Claude expired tokens)
	setSharedMenuItemVisible(mReauth, hasExpiredClaude, &s.reauthVisible, &s.reauthVisibleKnown)
	if mExtension != nil {
		setSharedMenuItemVisible(mExtension, hasClaude && offerExtension(extensionFolderExists(), claudeweb.LastExtensionContact(), time.Now()),
			&s.extensionVisible, &s.extensionVisibleKnown)
	}

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

	iconPick.update(activeResults, menus)

	// Update icon and title based on worst usage (only active providers)
	updateTrayIcon(activeResults)
	updateTrayTitle(activeResults)
	updateTrayTooltip(activeResults, displayNames)

	// Reset the independent deadline whenever API credit rows are rendered.
	scheduleAPICreditRows(results, menus, time.Now())

	// Check notification thresholds
	checkThresholds(activeResults, displayNames)
}

// Take the snapshot only after earlier native-menu renders have finished.
// The caller holds renderMu until its native-menu mutations are complete.
func lockLatestTrayState(renderMu sync.Locker) (map[string]*provider.UsageData, map[string]*status.ProviderStatus) {
	renderMu.Lock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResults, s.statuses
}

func updateResetCreditRow(menu *providerMenuItems, data *provider.UsageData, now time.Time) {
	title := resetCreditTraySummary(data, now)
	if title == "" {
		setMenuItemVisible(menu.resetItem, &menu.resetState, false)
		return
	}
	setMenuItemTitle(menu.resetItem, &menu.resetState, title)
	setMenuItemVisible(menu.resetItem, &menu.resetState, true)
}

func publishLocalResetSource(refreshing sync.Locker, registry *provider.Registry, sourceKey string, menus map[string]*providerMenuItems, mReauth *systray.MenuItem, iconPick *iconPicker, mEmpty, mProviderSetup *systray.MenuItem) bool {
	// Read and publish after any in-flight refresh has finished publishing.
	refreshing.Lock()
	defer refreshing.Unlock()

	p, ok := registry.Get(sourceKey)
	if !ok {
		return false
	}
	if registry.IsDisabled(p) {
		return true // saved; a disabled provider stays hidden
	}
	data, err := provider.FetchSource(context.Background(), p)
	if err != nil || data == nil {
		if err != nil {
			log.Printf("local reset source refresh failed: provider=%s", p.Name())
		}
		return false
	}
	name := provider.SourceKey(p)
	s.mu.Lock()
	results := make(map[string]*provider.UsageData, len(s.lastResults)+1)
	for key, value := range s.lastResults {
		results[key] = value
	}
	results[name] = data
	s.lastResults = results
	statuses := s.statuses
	s.mu.Unlock()
	if err := cache.UpdateProvider(name, data, provider.SourceRevision(p)); err != nil {
		log.Printf("local reset source cache update failed: provider=%s", p.Name())
	}
	updateUI(results, statuses, menus, mReauth, iconPick, mEmpty, mProviderSetup)
	return true
}

// iconPicker is the Settings → Tray icon submenu. Native menus cannot insert
// an item between existing ones, so each quota keeps the item it got when it
// first appeared, and the item is hidden while that quota has no data.
type iconPicker struct {
	parent  *systray.MenuItem
	auto    *systray.MenuItem
	targets map[iconTarget]*systray.MenuItem
	visible map[iconTarget]bool
	picked  chan iconTarget
}

func newIconPicker(settings *systray.MenuItem) *iconPicker {
	parent := settings.AddSubMenuItem("Tray icon", "")
	p := &iconPicker{
		parent:  parent,
		auto:    parent.AddSubMenuItemCheckbox("Automatic (most at risk)", "", true),
		targets: map[iconTarget]*systray.MenuItem{},
		visible: map[iconTarget]bool{},
		picked:  make(chan iconTarget, 1),
	}
	go p.forward(p.auto, iconTarget{})
	return p
}

func (p *iconPicker) forward(item *systray.MenuItem, target iconTarget) {
	for range item.ClickedCh {
		select {
		case p.picked <- target:
		default:
		}
	}
}

// update shows one checkable item per quota, in the provider menu's order, and
// checks the quota the icon follows. A pinned quota that disappears falls back
// to Automatic.
func (p *iconPicker) update(results map[string]*provider.UsageData, menus map[string]*providerMenuItems) {
	choices := pickerIconTargets(results, menus)

	s.mu.Lock()
	if !targetInChoices(s.iconTargetOverride, choices) {
		s.iconTargetOverride = iconTarget{}
	}
	override := s.iconTargetOverride
	s.mu.Unlock()

	if p == nil {
		return
	}
	displayNames := providerDisplayNames(menus)
	setMenuItemChecked(p.auto, override.Provider == "")
	shown := make(map[iconTarget]bool, len(choices))
	for _, target := range choices {
		shown[target] = true
		item := p.targets[target]
		if item == nil {
			item = p.parent.AddSubMenuItemCheckbox(pickerTargetLabel(results[target.Provider], target, displayNames), "", false)
			p.targets[target] = item
			go p.forward(item, target)
		}
		if !p.visible[target] {
			item.Show()
			p.visible[target] = true
		}
		setMenuItemChecked(item, target == override)
	}
	for target, item := range p.targets {
		if p.visible[target] && !shown[target] {
			item.Hide()
			p.visible[target] = false
		}
	}
}

// pickerTargetLabel names a quota the way its menu row does, for example
// "Codex: Weekly".
func pickerTargetLabel(data *provider.UsageData, target iconTarget, displayNames map[string]string) string {
	label := target.Window
	if window, _, ok := selectedIconWindow(data, target.Window); ok {
		label = meter.Label(window)
	}
	return providerDisplayName(target.Provider, displayNames) + ": " + label
}

func setMenuItemChecked(item *systray.MenuItem, checked bool) {
	if item.Checked() == checked {
		return
	}
	if checked {
		item.Check()
	} else {
		item.Uncheck()
	}
}

// pickerIconTargets lists the quotas a user can pin, ordered like the menu:
// providers in menu order, then each provider's windows in data order.
func pickerIconTargets(results map[string]*provider.UsageData, menus map[string]*providerMenuItems) []iconTarget {
	choices := manualIconTargets(results)
	rank := func(target iconTarget) (int, int) {
		order := len(menus)
		if menu := menus[target.Provider]; menu != nil {
			order = menu.order
		}
		if data := results[target.Provider]; data != nil {
			for i, window := range data.UsableWindows() {
				if window.Name == target.Window {
					return order, i
				}
			}
		}
		return order, 0
	}
	sort.SliceStable(choices, func(i, j int) bool {
		oi, wi := rank(choices[i])
		oj, wj := rank(choices[j])
		if oi != oj {
			return oi < oj
		}
		if choices[i].Provider != choices[j].Provider {
			return choices[i].Provider < choices[j].Provider
		}
		return wi < wj
	})
	return choices
}

func selectIconTarget(menus map[string]*providerMenuItems, picker *iconPicker, target iconTarget) {
	displayNames := providerDisplayNames(menus)

	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	s.mu.Lock()
	results := provider.PresentResetCreditSources(s.lastResults)
	s.iconTargetOverride = target
	s.mu.Unlock()

	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()
	picker.update(results, menus)
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

func activeIconTargets(results map[string]*provider.UsageData) []iconTarget {
	choices := activeIconTargetsAllowingStale(results, false)
	if len(choices) > 0 {
		return choices
	}
	return activeIconTargetsAllowingStale(results, true)
}

func manualIconTargets(results map[string]*provider.UsageData) []iconTarget {
	return activeIconTargetsAllowingStale(results, true)
}

func activeIconTargetsAllowingStale(results map[string]*provider.UsageData, allowStale bool) []iconTarget {
	if len(results) == 0 {
		return nil
	}
	type rankedTarget struct {
		target        iconTarget
		window        provider.UsageWindow
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
			ranked = append(ranked, rankedTarget{
				target:        target,
				window:        window,
				projection:    proj,
				hasProjection: true,
				score:         score,
			})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].hasProjection && ranked[j].hasProjection {
			if cmp := forecast.CompareWindowRisk(ranked[i].window, ranked[i].projection, ranked[j].window, ranked[j].projection); cmp != 0 {
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
	return forecast.Project(window.Utilization, window.ResetsAt, forecast.WindowLength(window))
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
	s.mu.Unlock()
	autoChoices := activeIconTargets(results)

	if override.Provider != "" {
		if targetInChoices(override, manualIconTargets(results)) {
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
	windowLen := forecast.WindowLength(window)
	return icons.MeterState{
		UsagePct:     window.Utilization,
		ExpectedPct:  meter.ExpectedPct(window.ResetsAt, windowLen),
		RiskPct:      windowRiskPct(window, proj),
		ShowExpected: true,
		Label:        windowBadgeLabelForWindow(window),
	}
}

// windowRiskPct keeps an exhausted current window at risk even when there
// is not enough elapsed time to project its usage.
func windowRiskPct(window provider.UsageWindow, proj forecast.Projection) float64 {
	if !proj.Stale && window.Utilization >= 100 && window.Utilization > proj.ProjectedPct {
		return window.Utilization
	}
	return proj.ProjectedPct
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
	return trayTitleForPlatform(runtime.GOOS, updateAvailable())
}

func trayTitleForPlatform(goos string, hasUpdate bool) string {
	// On macOS the title occupies menu-bar space beside the icon.
	if goos == "darwin" {
		return ""
	}
	if hasUpdate {
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
			windowLen := forecast.WindowLength(window)
			return window, forecast.Project(window.Utilization, window.ResetsAt, windowLen), true
		}
		return selected, selectedProj, false
	}
	hasSelected := false
	for _, window := range data.UsableWindows() {
		proj := windowProjection(window)
		if !hasSelected || forecast.CompareWindowRisk(window, proj, selected, selectedProj) < 0 {
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
	// A two-character badge cannot safely abbreviate a multi-digit duration.
	if len(label) > 1 && label[0] >= '0' && label[0] <= '9' && label[1] >= '0' && label[1] <= '9' {
		return "--"
	}
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
		return appendResetCreditTooltip(fmt.Sprintf("%s: %s", display, msg), data)
	}
	if data.Error != "" && !data.HasPresentableUsage() {
		return appendResetCreditTooltip(fmt.Sprintf("%s: %s", display, format.HumanizeError(data.Error)), data)
	}
	if data.Stale {
		reason := staleTooltipReason(data)
		if !data.FetchedAt.IsZero() {
			return appendResetCreditTooltip(fmt.Sprintf("%s: stale - showing last good data from %s (%s)", display, data.FetchedAt.Local().Format("Jan 2, 2006 3:04 PM"), reason), data)
		}
		return appendResetCreditTooltip(fmt.Sprintf("%s: stale - showing last good data (%s)", display, reason), data)
	}

	window, proj, ok := selectedIconWindow(data, windowName)
	if !ok {
		return appendResetCreditTooltip(display, data)
	}
	title := iconTooltipTitle(display, window)
	tooltip := compactIconTooltip(title, window, proj, windowExhaustion.ranOutAt(name, window, time.Now()))
	return appendResetCreditTooltip(tooltip, data)
}

func appendResetCreditTooltip(tooltip string, data *provider.UsageData) string {
	if resetLine := resetCreditTraySummary(data, time.Now()); resetLine != "" {
		return tooltip + "\n" + resetLine
	}
	return tooltip
}

func resetCreditTraySummary(data *provider.UsageData, now time.Time) string {
	if data == nil || data.ResetCredits == nil || (data.Stale && !data.ResetCredits.Snapshot) {
		return ""
	}
	return data.ResetCredits.Summary(now)
}

func staleTooltipReason(data *provider.UsageData) string {
	if data == nil || data.Warning == "" {
		return "usage unavailable"
	}
	return format.HumanizeError(data.Warning)
}

// compactIconTooltip lines up the title, run-out state, reset, and estimate.
// ranOutAt is when the tray saw the window run out, or zero if it cannot say.
func compactIconTooltip(title string, window provider.UsageWindow, proj forecast.Projection, ranOutAt time.Time) string {
	parts := make([]string, 0, 4)
	if title != "" {
		parts = append(parts, title)
	}
	switch {
	case proj.Unknown || proj.Stale:
		parts = append(parts, fmt.Sprintf("Current %.0f%%", window.Utilization))
	case proj.RunOutNote() != "":
		parts = append(parts, upperFirst(ranOutNote(proj.RunOutNote(), ranOutAt, time.Now())))
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

// trayMenuRows formats the quota rows of every account in one pass over the
// windows the menu shows. Each window is projected once, so the width of the
// margin column and the rows agree.
func trayMenuRows(results map[string]*provider.UsageData, menus map[string]*providerMenuItems) map[string][]string {
	type account struct {
		windows     []provider.UsageWindow
		projections []forecast.Projection
	}
	accounts := make(map[string]account, len(menus))
	var all []forecast.Projection
	for name, menu := range menus {
		data := results[name]
		if data == nil || data.Error != "" || data.IsExpired {
			continue
		}
		windows := meter.Shown(data.PresentationWindows())
		windows = windows[:min(len(windows), len(menu.windowItems))]
		projections := make([]forecast.Projection, len(windows))
		for i, window := range windows {
			if !window.ResetsAt.IsZero() {
				projections[i] = windowProjection(window)
			}
		}
		all = append(all, projections...)
		accounts[name] = account{windows, projections}
	}
	var shown []meter.Row
	byAccount := make(map[string][]meter.Row, len(accounts))
	for name, a := range accounts {
		accountRows := meter.Rows(a.windows, a.projections)
		for i := range accountRows {
			if accountRows[i].RunsOut == "now" {
				accountRows[i].RanOutAt = windowExhaustion.ranOutAt(name, a.windows[i], time.Now())
			}
		}
		byAccount[name] = accountRows
		shown = append(shown, byAccount[name]...)
	}
	widths := trayColumnWidths(shown)
	rows := make(map[string][]string, len(accounts))
	for name, accountRows := range byAccount {
		for _, row := range accountRows {
			rows[name] = append(rows[name], trayRowText(row, widths))
		}
	}
	return rows
}

// figureSpace is as wide as a digit, so padded numbers line up in the
// menu's proportional font.
const figureSpace = "\u2007"

// trayGutter separates a row's columns. Columns line up, so they need space
// between them, not punctuation.
const trayGutter = "    "

// trayWidths are the widest values in the menu, in runes, so each column
// starts at the same place on every row: an early forecast such as "3360%"
// widens its column instead of pushing its own row out of line. Times are
// not padded: their letters are not as wide as digits, and they come last.
type trayWidths struct {
	used, forecast int
}

func trayColumnWidths(rows []meter.Row) trayWidths {
	widths := trayWidths{used: len("100%"), forecast: len("100%")}
	for _, row := range rows {
		widths.forecast = max(widths.forecast, len([]rune(row.Forecast)))
		widths.used = max(widths.used, len([]rune(row.Used)))
	}
	return widths
}

// trayRowText writes a row for a native menu, which has no color and no
// columns: labeled values padded with figure spaces and separated by
// gutters. Research: divan ai-quota-monitoring/compact-quota-row-
// typography-for-menus-and-terminal-tables.md.
func trayRowText(row meter.Row, widths trayWidths) string {
	if row.Line != "" {
		return row.Line
	}
	bar := meter.BarText(row.Bar)
	if !row.HasPace() {
		bar += figureSpace // as wide as the pace mark, so the columns still line up
	}
	forecast := "no forecast"
	switch {
	case row.Forecast != "":
		forecast = meter.Pad(row.Forecast, widths.forecast, figureSpace) + " at reset"
	case row.ForecastState != "":
		forecast = "forecast " + row.ForecastState
	}
	resets := row.Resets
	if !row.ResetPolicy {
		resets = "resets in " + row.Resets
	}
	columns := []string{row.Code, bar, meter.Pad(row.Used, widths.used, figureSpace) + " used", forecast, resets}
	switch row.RunsOut {
	case "":
	case "now":
		columns = append(columns, ranOutLabel(row.RanOutAt, time.Now()))
	default:
		columns = append(columns, "runs out in "+row.RunsOut)
	}
	for _, detail := range []string{row.Qualifier, row.Money} {
		if detail != "" {
			columns = append(columns, detail)
		}
	}
	return strings.Join(columns, trayGutter)
}

// windowRunsOutBeforeReset reports whether a window is projected to run out
// before it resets; its row gets the red risk mark.
func windowRunsOutBeforeReset(window provider.UsageWindow) bool {
	if window.ResetsAt.IsZero() {
		return false
	}
	proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.WindowLength(window))
	if proj.Stale {
		return false
	}
	// A window used up before its forecast warms up has already run out.
	return window.Utilization >= 100 || !proj.Unknown && !proj.WillLastToReset
}

func compactProjectionEstimate(proj forecast.Projection) string {
	if proj.Unknown || proj.Stale {
		return proj.PaceIndicator()
	}
	estimate := forecast.PaceLabel(proj.ProjectedPct)
	if strings.HasPrefix(estimate, "est.") {
		return "Est." + strings.TrimPrefix(estimate, "est.")
	}
	return estimate
}

var sendThresholdNotification = notify

func checkThresholds(results map[string]*provider.UsageData, displayNames map[string]string) {
	type notification struct{ title, body, urgency string }
	var pending []notification
	now := time.Now()
	s.mu.Lock()
	if s.thresholdUtilization == nil {
		s.thresholdUtilization = make(map[iconTarget]thresholdSample)
	}
	warningThreshold, criticalThreshold := 80.0, 95.0
	if cfg != nil {
		warningThreshold = cfg.Settings.NotificationThresholds.Warning
		criticalThreshold = cfg.Settings.NotificationThresholds.Critical
	}
	for name, data := range results {
		if data == nil || data.Error != "" || data.Stale || data.IsExpired {
			continue
		}
		for _, window := range data.UsableWindows() {
			if !window.ResetsAt.IsZero() && !window.ResetsAt.After(now) {
				continue // A passed reset is stale data, not a new threshold crossing.
			}
			key := iconTarget{Provider: name, Window: window.Name}
			previous := s.thresholdUtilization[key]
			pct, oldPct := window.Utilization, previous.Utilization
			// A new reset period rearms thresholds even if low samples were missed.
			if newResetPeriod(previous.ResetsAt, window.ResetsAt, now) {
				oldPct = 0
			}
			s.thresholdUtilization[key] = thresholdSample{Utilization: pct, ResetsAt: window.ResetsAt}
			display := displayNames[name]
			if display == "" {
				display = name
			}
			proj := forecast.Project(pct, window.ResetsAt, forecast.WindowLength(window))
			message := fmt.Sprintf("%s window at %.0f%% — %s", window.Name, pct, proj.PaceIndicator())
			if pct >= criticalThreshold && oldPct < criticalThreshold {
				pending = append(pending, notification{fmt.Sprintf("%s usage critical", display), message, "critical"})
			} else if pct >= warningThreshold && oldPct < warningThreshold {
				pending = append(pending, notification{fmt.Sprintf("%s usage warning", display), message, "normal"})
			}
		}
	}
	s.mu.Unlock()
	for _, n := range pending {
		sendThresholdNotification(n.title, n.body, n.urgency)
	}
}

func setErrorState(msg string) {
	setIconByName("gray", icons.Gray)
	systray.SetTitle(trayTitleForPlatform(runtime.GOOS, false))
	systray.SetTooltip("")
}

func updateAutostartLabel(m *systray.MenuItem) {
	if !autostart.IsSupported() {
		m.SetTitle("Launch at login (unsupported on this OS)")
		m.Disable()
		return
	}
	setMenuItemChecked(m, autostart.IsInstalled())
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

// mExtension offers the browser extension that reads Claude resets and API
// credits; updateUI shows it while offerExtension says so.
var mExtension *systray.MenuItem

const extensionMenuTitle = "Connect your browser for Claude resets and API credits…"

// offerExtension reports whether the menu should offer the extension: it has
// not reached Clawmeter in the past week, and the user has never opened its
// setup, which writes the folder. Someone who chose not to install it is not
// asked again; Settings keeps the way back.
func offerExtension(folderExists bool, lastContact, now time.Time) bool {
	return !folderExists && (lastContact.IsZero() || now.Sub(lastContact) > 7*24*time.Hour)
}

func extensionFolderExists() bool {
	dir, err := browserext.Dir()
	if err != nil {
		return false
	}
	_, err = os.Stat(dir)
	return err == nil
}

// openExtensionSetup installs the extension folder and opens the setup page,
// which shows the folder.
func openExtensionSetup() {
	dir, err := browserext.Install()
	if err != nil {
		log.Printf("browser extension setup: %v", err)
		notify("Clawmeter", "Couldn't set up the browser extension: "+err.Error(), "normal")
		return
	}
	openURL(browserext.SetupURL(dir))
}

// issueURL opens a new GitHub issue that names this build and desktop. It
// carries nothing about the user's accounts or usage.
func issueURL(version, platform, desktop string) string {
	build := "Clawmeter " + version + " · " + platform
	if desktop != "" {
		build += " · " + desktop
	}
	body := "**What happened**\n\n\n**What you expected**\n\n\n---\n" + build + "\n"
	return "https://github.com/tnunamak/clawmeter/issues/new?body=" + url.QueryEscape(body)
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
	seen := make(map[string]struct{}, len(providers))
	for _, p := range providers {
		name := provider.FamilyName(p)
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
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
			if !provider.UsageDataMatchesSource(data, p.Name(), provider.SourceID(p)) {
				continue
			}
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
			if cmp := forecast.CompareWindowRisk(si.window, si.projection, sj.window, sj.projection); cmp != 0 {
				return cmp < 0
			}
		}
		return keys[i] < keys[j] // alphabetical tiebreak
	})
	return keys
}

type providerSeverityRank struct {
	tier          int
	window        provider.UsageWindow
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
	window, proj, ok := selectedIconWindow(data, "")
	return providerSeverityRank{tier: 2, window: window, projection: proj, hasProjection: ok}
}

// newResetPeriod reports whether a window's reset time starts a new period.
// Providers that derive the reset from time remaining move it by a few
// seconds between polls, so only a later reset after the previous one passed
// or a large forward jump counts.
func newResetPeriod(previous, current, now time.Time) bool {
	if !current.After(previous) {
		return false
	}
	if previous.IsZero() {
		return true
	}
	return !now.Before(previous) || current.Sub(previous) > 30*time.Minute
}

// nextAPICreditDeadline excludes old bookmark snapshots and already stale/signed-out pools.
func nextAPICreditDeadline(results map[string]*provider.UsageData, now time.Time) time.Time {
	var earliest time.Time
	for _, data := range results {
		if data == nil {
			continue
		}
		for _, pool := range data.CreditPools {
			if pool.ObservationSource != "extension" || !pool.IsLive(now) {
				continue
			}
			deadline := pool.ObservedAt.Add(5*time.Minute + time.Second)
			if earliest.IsZero() || deadline.Before(earliest) {
				earliest = deadline
			}
		}
	}
	return earliest
}

// Called under trayRenderMu. The callback only redraws the displayed credit
// rows from local snapshots. It never takes refreshing or probes providers.
func scheduleAPICreditRows(results map[string]*provider.UsageData, menus map[string]*providerMenuItems, now time.Time) {
	apiCreditsTimerGeneration++
	generation := apiCreditsTimerGeneration
	if apiCreditsTimer != nil {
		apiCreditsTimer.Stop()
	}
	deadline := nextAPICreditDeadline(results, now)
	if deadline.IsZero() {
		return
	}
	apiCreditsTimer = time.AfterFunc(time.Until(deadline), func() {
		latest, _ := lockLatestTrayState(&trayRenderMu)
		defer trayRenderMu.Unlock()
		if generation != apiCreditsTimerGeneration {
			return
		}
		results := provider.PresentResetCreditSources(latest)
		systray.BeginMenuUpdate()
		defer systray.EndMenuUpdate()
		for name, menu := range menus {
			data := results[name]
			if !menu.headerState.visible || data == nil || len(data.CreditPools) == 0 {
				continue
			}
			balances := data.Balances
			if data.IsExpired || data.Error != "" {
				balances = nil
			}
			showBalanceRows(menu, data, balances)
		}
		scheduleAPICreditRows(results, menus, time.Now())
	})
}
