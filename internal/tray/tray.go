//go:build tray

package tray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	claudewebprovider "github.com/tnunamak/clawmeter/internal/provider/claudeweb"
	"github.com/tnunamak/clawmeter/internal/resetfeed"
	"github.com/tnunamak/clawmeter/internal/shellpath"
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
	"github.com/tnunamak/clawmeter/internal/update"
	"github.com/tnunamak/clawmeter/internal/view"
)

const (
	updateCheckInterval    = 30 * time.Minute
	tokenPlanProviderName  = "alibaba_token"
	retryConnectionAction  = "Retry quota access"
	setupExtensionAction   = "Set up the browser extension…"
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
	// feedReading is the last codex-resets.com reading, set by refresh. It is
	// empty when the setting is off or no Codex source is configured.
	feedReading resetfeed.Reading
}

type iconTarget struct {
	Provider string `json:"account_key"`
	Window   string `json:"window"`
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
		mError := systray.AddMenuItem("Configuration needs attention…", "Show the configuration file location and repair steps; retrying automatically")
		go func() {
			for range mError.ClickedCh {
				openRecovery(configRecoveryAction(), openURL)
			}
		}()
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
	s.iconTargetOverride = loadIconPin()
	s.mu.Unlock()
	s.failureGate = provider.NewFailureGate()

	// Build the initial native menu as one transaction. On Linux, each menu
	// mutation otherwise emits a separate D-Bus layout signal while Plasma is
	// trying to register the tray item.
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()

	// Launch at login is opt-in: the user enables it from the tray menu.
	// Do not silently persist anything on first run.

	setIconByName("logo", icons.App)
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
	providerHideRequests := make(chan string, 1)
	providerShowRequests := make(chan string, 1)
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
		menu.disabled = cfg.IsProviderDisabled(provider.FamilyName(p))
		hideProviderMenu(menu)
		providerMenus[provider.SourceKey(p)] = menu
	}

	// Placeholder shown when no provider is in the primary UI; updateUI
	// toggles its visibility based on whether any menu has data to render.
	mEmpty := systray.AddMenuItem("No active providers", "")
	mEmpty.Disable()
	mProviderSetup := systray.AddMenuItem("Set up providers…", "Show detected providers and connection steps")
	mProviderSetup.Hide()
	s.emptyVisible = true
	s.emptyVisibleKnown = true
	s.providerSetupVisible = false
	s.providerSetupVisibleKnown = true

	systray.AddSeparator()

	// Kept hidden for the shared render signature. Sign-in actions belong on
	// their own account's header rather than a global, ambiguous instruction.
	mReauth := systray.AddMenuItem("", "")
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
	iconPick := newIconPicker()
	for _, menu := range providerMenus {
		iconPick.forwardRows(menu)
		forwardHide(menu, providerHideRequests)
	}
	mAutostart := mSettings.AddSubMenuItemCheckbox("Launch at login", "", false)
	updateAutostartLabel(mAutostart)
	mSetupExtension := mSettings.AddSubMenuItem("Set up the browser extension…", "")
	mReport := mSettings.AddSubMenuItem("Report a problem…", "")
	showHidden := newShowHiddenMenu(mSettings, providerMenus)
	showHidden.forward(providerShowRequests)
	showHidden.update(providerMenus)
	mSettings.AddSubMenuItem(fmt.Sprintf("Clawmeter %s", version), "").Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "")

	ticker := time.NewTicker(config.PollInterval(loaded.Settings.PollInterval))
	pollIntervalSeconds := loaded.Settings.PollInterval
	windowExhaustion.load(exhaustionPath(), 2*config.PollInterval(pollIntervalSeconds))

	// reloadConfig re-reads config.yaml from disk and propagates changes
	// to the registry filter and per-provider menu state. This is what
	// makes `clawmeter config enable <provider>` (or any other out-of-band
	// edit) reflect in the running tray without a restart.
	// enablementMu keeps a reload from applying a config read before a
	// Hide or Show click saved its change.
	var enablementMu sync.Mutex
	applyConfig := func(newCfg *config.Config) {
		s.mu.Lock()
		cfg = newCfg
		s.mu.Unlock()
		registry.SetEnabledFilter(newCfg)
		applyProviderEnablement(providerMenus, newCfg)
		trayRenderMu.Lock()
		showHidden.update(providerMenus)
		trayRenderMu.Unlock()
	}
	reloadConfig := func() {
		enablementMu.Lock()
		defer enablementMu.Unlock()
		newCfg, err := config.Load(all.SourceValidator())
		if err != nil {
			return
		}
		applyConfig(newCfg)
		pollIntervalSeconds = resetPollIntervalIfChanged(pollIntervalSeconds, newCfg.Settings.PollInterval, ticker.Reset)
	}

	refresher := &usageRefresher{
		registry:     registry,
		menus:        providerMenus,
		reloadConfig: reloadConfig,
		pollSeconds:  func() int { return pollIntervalSeconds },
		render: func(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus) {
			updateUI(results, statuses, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
		},
		finished: func(at time.Time) {
			trayRenderMu.Lock()
			mRefresh.SetTitle(fmt.Sprintf("Refresh Now  (updated %s)", at.Format("15:04")))
			trayRenderMu.Unlock()
		},
	}
	// Refresh usage only (lightweight, runs every poll cycle). It reports
	// false when another refresh was already running.
	refresh := refresher.refresh

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

	var updater updateAction
	var updateMenu updateMenuState
	var updateHelp recoveryAction // guarded by trayRenderMu

	// Checks publish only while their original menu action is still current.
	checkUpdate := func() {
		if !updateChecking.TryLock() {
			return
		}
		defer updateChecking.Unlock()
		contact := lastExtensionContact()
		updateMenu.check(func() (*update.Release, error) {
			s.mu.Lock()
			currentCfg := cfg
			s.mu.Unlock()
			if !currentCfg.ShouldCheckForUpdates() {
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return update.Check(ctx, version)
		}, func(rel *update.Release) {
			setPendingRelease(rel)
			if rel == nil {
				mUpdate.Hide()
			} else {
				mUpdate.SetTitle(fmt.Sprintf("• Update to %s", rel.Version))
				mUpdate.Show()
			}
			renderIconAndTooltip(providerMenus, contact)
		})
	}

	// Retry the action shown on the row: apply, restart, or package-manager help.
	applyUpdate := func() {
		var rel *update.Release
		var help recoveryAction
		var skip bool
		if !updateMenu.begin(func() {
			help = updateHelp
			rel = currentPendingRelease()
			skip = help.help.Title == "" && rel == nil && !updater.restartPending()
			if help.help.Title == "" && !skip {
				mUpdate.SetTitle("Updating…")
				mUpdate.Disable()
			}
		}) {
			return
		}
		if skip {
			updateMenu.finish(false, func() {})
			return
		}
		if help.help.Title != "" {
			updateMenu.finish(true, func() {})
			openRecovery(help, openURL)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, started := updater.run(ctx, rel, updateOperations{
			ExecutablePath: update.ExecutablePath, ApplyTo: update.ApplyTo, Restart: update.Restart,
		})
		if !started {
			updateMenu.finish(true, func() {})
			return
		}
		managed := errors.Is(out.Err, update.ErrHomebrewManaged) || errors.Is(out.Err, update.ErrHomebrewReceiptUnverified)
		restartPending := updater.restartPending()
		updateMenu.finish(out.Quit || restartPending || managed, func() {
			if managed {
				updateHelp = updateRecoveryError(out.Err)
				out.Title = "Homebrew installation · Show update steps…"
			} else if out.Err != nil && !restartPending {
				out.Title = "Update failed · Retry update"
			}
			mUpdate.SetTitle(out.Title)
			if out.Err != nil {
				mUpdate.SetTooltip(out.Err.Error())
			} else {
				mUpdate.SetTooltip("")
			}
			if !out.Quit {
				mUpdate.Enable()
				mUpdate.Show()
			}
		})
		if out.Err != nil {
			notify("Clawmeter", out.Err.Error(), "normal")
		}
		if out.Quit {
			if out.Updated && rel != nil {
				notify("Clawmeter", fmt.Sprintf("Updated to %s — restarting", rel.Version), "low")
			}
			systray.Quit()
		}
	}

	var manualRefresh manualRefreshAction
	requestRefresh := func() {
		manualRefresh.request(func() {
			trayRenderMu.Lock()
			mRefresh.SetTitle("Refreshing…")
			trayRenderMu.Unlock()
			refresher.refreshWhenFree(true)
			refreshStatus()
			checkUpdate()
		})
	}
	for _, menu := range providerMenus {
		menu.refreshUsage = requestRefresh
	}

	// Show cached data immediately while fetching fresh data
	if cached, err := cache.Read(); err == nil && cached != nil {
		// Do not probe credentials while the native menu is being registered.
		// GetConfigured calls every provider's IsConfigured method, and some
		// providers recover PATH through a login shell. That work belongs to the
		// background refresh; cached rendering only needs the registered names.
		allProviders := registry.GetAll()
		filtered, accepted := cachedResultsForCurrentSources(cached, allProviders)
		s.mu.Lock()
		s.lastResults = filtered
		// The revisions the cached readings were accepted under, not a second
		// read: a login that changed in between must not relabel them.
		s.lastSourceRevisions = accepted
		s.lastRefreshAt = cached.FetchedAt
		s.mu.Unlock()
		updateUI(filtered, nil, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
	}

	extensionCtx, stopExtension := context.WithCancel(context.Background())
	publishAPICredits := func() {
		publishLocalResetSource(&refresher.mu, registry, "claude_api", providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
	}
	go func() {
		err := claudeweb.ServeExtension(extensionCtx, func() []claudeweb.Account {
			return claudewebprovider.Accounts(registry.GetFamily("claude"))
		}, func(result claudeweb.Result) {
			if result.IsResets() {
				publishLocalResetSource(&refresher.mu, registry, claudeWebProviderKey(result.Resets.ResetCreditsTarget), providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
				return
			}
			publishAPICredits()
		}, browserext.Token, func() {
			publishLocalResetSource(&refresher.mu, registry, "openai_api", providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
		})
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
	// changeEnablement saves a provider on/off change, then redraws at once
	// instead of waiting for the next poll.
	changeEnablement := func(save func() (*config.Config, error)) bool {
		return changeProviderEnablement(&enablementMu, save, applyConfig, func() {
			updateUI(nil, nil, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
		})
	}

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
				requestRefresh()
			case <-mProviderSetup.ClickedCh:
				go openRecovery(providerSetupAction(), openURL)
			case family := <-providerHideRequests:
				go changeEnablement(func() (*config.Config, error) {
					return saveHidden(family)
				})
			case family := <-providerShowRequests:
				go showHiddenProvider(family, &enablementMu, applyConfig, func() {
					updateUI(nil, nil, providerMenus, mReauth, iconPick, mEmpty, mProviderSetup)
				}, refresher.refreshWhenFree)
			case target := <-iconPick.picked:
				selectIconTarget(providerMenus, iconPick, target)
			case providerName := <-providerConnectActions:
				menu := providerMenus[providerName]
				if menu == nil || !menu.connectable {
					continue
				}
				trayRenderMu.Lock()
				allowed := menu.headerState.visible && menu.headerState.enabled && menu.headerConnects.Load() && !menu.disabled
				trayRenderMu.Unlock()
				if !allowed {
					continue
				}
				if !menu.connecting.CompareAndSwap(false, true) {
					continue
				}
				trayRenderMu.Lock()
				setProviderConnectionItemLocked(menu, "", true)
				trayRenderMu.Unlock()
				go func(providerName string, menu *providerMenuItems) {
					err := connectProviderFromTray(menu.provider.Name())
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

// completedProblem reports whether a block shows a problem found by a
// finished check: an error or an expired login in a reading, or a setup status
// that needs sign-in. A block with no reading yet, as at startup before the
// first poll ends, is not a problem.
func completedProblem(data *provider.UsageData, setup provider.SetupStatus) bool {
	if data == nil {
		return setup.State == provider.SetupNeedsAuth
	}
	return data.IsExpired || data.Error != ""
}

// hideOffered reports whether the block for source key gets a Hide item: it
// has a completed problem, and so does every other shown source of its family.
// Hiding writes `enabled: false` for the whole family, so on a row of a family
// with a working account (one of several Claude accounts failing while the
// others work) it would also hide the working accounts. A sibling that has no
// reading yet may turn out to work, so it blocks Hide too. No Hide is offered
// there; the user can fix or remove the failing account instead.
func hideOffered(key string, data *provider.UsageData, menus map[string]*providerMenuItems, shown map[string]*provider.UsageData, setups map[string]provider.SetupStatus) bool {
	if !completedProblem(data, setups[key]) {
		return false
	}
	family := provider.FamilyName(menus[key].provider)
	for other, menu := range menus {
		if other == key || provider.FamilyName(menu.provider) != family {
			continue
		}
		if sibling, ok := shown[other]; ok && !completedProblem(sibling, setups[other]) {
			return false
		}
	}
	return true
}

// forwardHide sends the family of a provider block to requests when its Hide
// item is clicked.
func forwardHide(menu *providerMenuItems, requests chan<- string) {
	family := provider.FamilyName(menu.provider)
	go func() {
		for range menu.hideItem.ClickedCh {
			select {
			case requests <- family:
			default:
			}
		}
	}()
}

// saveHidden writes the change `clawmeter providers disable <family>` makes.
func saveHidden(family string) (*config.Config, error) {
	return config.SetProviderEnabled(family, false, all.SourceValidator())
}

// saveShown writes the change `clawmeter providers enable <family>` makes.
func saveShown(family string) (*config.Config, error) {
	return config.SetProviderEnabled(family, true, all.SourceValidator())
}

// changeProviderEnablement saves a provider on/off change, applies the saved
// config and redraws at once instead of waiting for the next poll.
func changeProviderEnablement(mu sync.Locker, save func() (*config.Config, error), apply func(*config.Config), render func()) bool {
	mu.Lock()
	defer mu.Unlock()
	newCfg, err := save()
	if err != nil {
		log.Printf("save provider setting: %v", err)
		notify("Clawmeter", "Could not save the change. Try again.", "normal")
		return false
	}
	apply(newCfg)
	render()
	return true
}

// showHiddenProvider turns a hidden family back on, redraws, and then reads
// it, because a hidden provider was not polled. The read must not be skipped
// when a refresh is running: that refresh started before the change, left the
// provider out, and would leave its row empty until the next poll.
func showHiddenProvider(family string, mu sync.Locker, apply func(*config.Config), render func(), refresh func(force bool)) bool {
	if !changeProviderEnablement(mu, func() (*config.Config, error) { return saveShown(family) }, apply, render) {
		return false
	}
	refresh(true)
	return true
}

// showHiddenMenu is the Settings submenu "Show hidden providers": one item per
// provider family that has a menu, shown while config turns that family off.
// The fork cannot insert items, so every item is pre-created and hidden.
type showHiddenMenu struct {
	parent      *systray.MenuItem
	parentState menuItemState
	items       []*hiddenFamilyItem
}

type hiddenFamilyItem struct {
	family string
	item   *systray.MenuItem
	state  menuItemState
}

// newShowHiddenMenu adds the submenu under settings, hidden, with an item for
// each family among menus, ordered by display name.
func newShowHiddenMenu(settings *systray.MenuItem, menus map[string]*providerMenuItems) *showHiddenMenu {
	names := make(map[string]string)
	for _, menu := range menus {
		family := provider.FamilyName(menu.provider)
		if _, ok := names[family]; !ok {
			names[family] = menu.provider.DisplayName()
		}
	}
	families := make([]string, 0, len(names))
	for family := range names {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool {
		a, b := strings.ToLower(names[families[i]]), strings.ToLower(names[families[j]])
		if a != b {
			return a < b
		}
		return families[i] < families[j]
	})
	m := &showHiddenMenu{parent: settings.AddSubMenuItem("Show hidden providers", "")}
	for _, family := range families {
		item := m.parent.AddSubMenuItem(names[family], "")
		item.Hide()
		m.items = append(m.items, &hiddenFamilyItem{family: family, item: item, state: menuItemState{title: names[family], enabled: true, initialized: true}})
	}
	m.parent.Hide()
	m.parentState = menuItemState{title: "Show hidden providers", enabled: true, initialized: true}
	return m
}

// forward sends the family of a clicked item to requests.
func (m *showHiddenMenu) forward(requests chan<- string) {
	for _, entry := range m.items {
		go func() {
			for range entry.item.ClickedCh {
				select {
				case requests <- entry.family:
				default:
				}
			}
		}()
	}
}

// update shows the item of each family that config turns off, and the
// submenu while any item is shown. The caller holds trayRenderMu.
func (m *showHiddenMenu) update(menus map[string]*providerMenuItems) {
	anyShown := false
	for _, entry := range m.items {
		hidden := false
		for _, menu := range menus {
			if menu.disabled && provider.FamilyName(menu.provider) == entry.family {
				hidden = true
			}
		}
		setMenuItemVisible(entry.item, &entry.state, hidden)
		anyShown = anyShown || hidden
	}
	setMenuItemVisible(m.parent, &m.parentState, anyShown)
}

func applyProviderEnablement(menus map[string]*providerMenuItems, cfg *config.Config) {
	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	for _, menu := range menus {
		menu.explicitlyEnabled = cfg.IsProviderExplicitlyEnabled(provider.FamilyName(menu.provider))
		menu.disabled = cfg.IsProviderDisabled(provider.FamilyName(menu.provider))
	}
}

// providerMenuItems holds menu items for a single provider.
type providerMenuItems struct {
	provider     provider.Provider
	displayName  string
	headerItem   *systray.MenuItem
	statusItem   *systray.MenuItem
	resetItem    *systray.MenuItem
	announceItem *systray.MenuItem
	// hideItem turns the provider off in config. It is shown only while the
	// block reports a problem and no healthy account shares the family.
	hideItem       *systray.MenuItem
	connectable    bool // the name row can run the quota connection
	headerAction   string
	headerRecovery recoveryAction
	sourceRevision string // login used by the drawn recovery action; guarded by trayRenderMu
	resetURL       string
	statusURL      string
	refreshUsage   func()
	extensionReady bool
	headerConnects atomic.Bool
	windowItems    []*systray.MenuItem
	// moreItem says how many windows the rows above leave out.
	moreItem          *systray.MenuItem
	balanceItems      []*systray.MenuItem
	localModelsItem   *systray.MenuItem
	localModelItems   []*systray.MenuItem
	localModelStates  []menuItemState
	localModelsState  menuItemState
	separator         *systray.MenuItem
	order             int // position in the menu, top first
	opensDashboard    bool
	everHealthy       bool // true once we've seen useful quota data
	explicitlyEnabled bool
	disabled          bool // turned off in config; never shown
	connecting        atomic.Bool
	connectFailed     atomic.Bool // last tray connection attempt failed
	connectProblem    string      // sanitized setup error; guarded by trayRenderMu
	headerState       menuItemState
	statusState       menuItemState
	resetState        menuItemState
	announceState     menuItemState
	hideState         menuItemState
	announceURL       string // source post of the shown announcement; guarded by trayRenderMu
	windowStates      []menuItemState
	windowAtRisk      []bool
	windowIcons       []string     // per quota row: the icon shown; guarded by trayRenderMu
	windowTargets     []iconTarget // per quota row; guarded by trayRenderMu
	moreState         menuItemState
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
	var localModelsItem *systray.MenuItem
	var localModelItems []*systray.MenuItem
	var localModelStates []menuItemState
	if p.Name() == "opencode_free" {
		localModelsItem = systray.AddMenuItem("Free models · local observations", "Local activity observations; these do not indicate quota or request availability")
		// Plasma treats children-display as immutable when importing a row.
		// Establish the submenu before the first export, not after polling.
		placeholder := localModelsItem.AddSubMenuItem("", "")
		placeholder.Disable()
		placeholder.Hide()
		localModelItems = []*systray.MenuItem{placeholder}
		localModelStates = []menuItemState{{visible: false, enabled: false, initialized: true}}
		localModelsItem.Disable()
		localModelsItem.Hide()
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
	moreItem := systray.AddMenuItem("", "")
	moreItem.Disable()
	moreItem.Hide()
	setMenuItemIcon(moreItem, icons.MenuBlank())
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
	resetItem := systray.AddMenuItem("", "Open Claude Usage in the browser; make sure it is signed in to this source’s account")
	resetItem.Disable()
	setMenuItemIcon(resetItem, icons.MenuBlank())
	resetItem.Hide()
	resetState := menuItemState{visible: false, enabled: false, initialized: true}

	// An announced Codex reset sits after the reset-credit row and opens its
	// source post when clicked.
	announceItem := systray.AddMenuItem("", "")
	setMenuItemIcon(announceItem, icons.MenuBlank())
	announceItem.Hide()

	// "Hide <name>" ends the block. It acts on the whole provider family, as
	// `clawmeter providers disable` does.
	hideItem := systray.AddMenuItem("Hide "+p.DisplayName(), "")
	setMenuItemIcon(hideItem, icons.MenuBlank())
	hideItem.Hide()

	menu := &providerMenuItems{
		provider:          p,
		displayName:       displayName,
		headerItem:        header,
		statusItem:        statusItem,
		resetItem:         resetItem,
		announceItem:      announceItem,
		hideItem:          hideItem,
		connectable:       connectable,
		windowItems:       windowItems,
		moreItem:          moreItem,
		moreState:         menuItemState{visible: false, enabled: false, initialized: true},
		balanceItems:      balanceItems,
		localModelsItem:   localModelsItem,
		localModelItems:   localModelItems,
		localModelStates:  localModelStates,
		separator:         separator,
		opensDashboard:    opensDashboard,
		explicitlyEnabled: explicitlyEnabled,
		headerState:       menuItemState{title: displayName, visible: true, enabled: opensDashboard || connectable, initialized: true},
		statusState:       menuItemState{title: "Loading...", visible: true, enabled: false, initialized: true},
		resetState:        resetState,
		announceState:     menuItemState{visible: false, enabled: true, initialized: true},
		hideState:         menuItemState{title: "Hide " + p.DisplayName(), visible: false, enabled: true, initialized: true},
		windowStates:      windowStates,
		windowAtRisk:      make([]bool, len(windowItems)),
		windowIcons:       make([]string, len(windowItems)),
		windowTargets:     make([]iconTarget, len(windowItems)),
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
		for range announceItem.ClickedCh {
			openAnnouncement(menu, openURL)
		}
	}()
	go func() {
		for range resetItem.ClickedCh {
			openResetRow(menu, openURL)
		}
	}()
	go func() {
		for range statusItem.ClickedCh {
			openStatusRow(menu, openURL)
		}
	}()
	go func() {
		for range header.ClickedCh {
			openProviderHeader(menu, openURL, func(key string) {
				select {
				case connectActions <- key:
				default:
				}
			})
		}
	}()

	return menu
}

// lastExtensionContact is a variable so tests can set the browser contact.
var lastExtensionContact = claudeweb.LastExtensionContact

// openAnnouncement opens the source post of the announcement on the row.
func openAnnouncement(menu *providerMenuItems, open func(string)) {
	trayRenderMu.Lock()
	target, revision := menu.announceURL, menu.sourceRevision
	visible := menu.announceState.visible && menu.announceState.enabled && !menu.disabled
	trayRenderMu.Unlock()
	if visible && sourceStillCurrent(menu, revision) && target != "" {
		open(target)
	}
}

// openBalanceRow opens the Console for a balance row that carries an action.
func openBalanceRow(menu *providerMenuItems, row int, open func(string)) {
	openBalanceRowWithSetup(menu, row, open, openExtensionSetup)
}

func openBalanceRowWithSetup(menu *providerMenuItems, row int, open func(string), setup func()) {
	trayRenderMu.Lock()
	if row < 0 || row >= len(menu.balanceActions) {
		trayRenderMu.Unlock()
		return
	}
	action, revision := menu.balanceActions[row], menu.sourceRevision
	visible := menu.balanceStates[row].visible && menu.balanceStates[row].enabled && !menu.disabled
	trayRenderMu.Unlock()
	if visible && sourceStillCurrent(menu, revision) && action != "" {
		if action == setupExtensionAction {
			setup()
		} else if action == provider.OpenAIAPIBillingURL {
			open(provider.OpenAIAPIBillingURL)
		} else {
			open(claudeweb.ConsoleURL)
		}
	}
}

func setProviderSeparatorVisible(menu *providerMenuItems, visible bool) {
	setMenuItemVisible(menu.separator, &menu.separatorState, visible)
}

func hideProviderMenu(menu *providerMenuItems) {
	menu.headerRecovery = recoveryAction{}
	menu.resetURL, menu.announceURL, menu.statusURL = "", "", ""
	menu.headerConnects.Store(false)
	setMenuItemVisible(menu.headerItem, &menu.headerState, false)
	setMenuItemVisible(menu.statusItem, &menu.statusState, false)
	setMenuItemVisible(menu.resetItem, &menu.resetState, false)
	setMenuItemVisible(menu.announceItem, &menu.announceState, false)
	setMenuItemVisible(menu.hideItem, &menu.hideState, false)
	hideProviderWindows(menu)
	setProviderSeparatorVisible(menu, false)
}

func showBalanceRows(menu *providerMenuItems, acct *view.Account, balances []provider.UsageBalance, now time.Time) int {
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
	if acct != nil {
		for _, balance := range acct.APICreditBalances {
			show(balance.Text, balance.ActionURL)
		}
		for _, benefit := range acct.CreditBenefits {
			if benefit.Text != "" {
				show(benefit.Text, benefit.ActionURL)
			}
		}
		for _, grant := range acct.CreditGrants {
			show(grant.Summary(), "")
		}
		for _, pool := range acct.Pools {
			title := pool.TrayLine(now)
			// A lone pool on the Claude API row needs no label; anywhere else it
			// sits among other rows.
			if acct.Provider != provider.ClaudeAPISourceName || len(acct.Pools) > 1 {
				title = pool.Name + ": " + title
			}
			action := pool.Action(now)
			if action != "" && pool.ObservationSource == "extension" && pool.ExtensionContactAt.IsZero() && !menu.extensionReady {
				title = strings.TrimSuffix(title, action) + setupExtensionAction
				action = setupExtensionAction
			}
			show(title, action)
		}
		showLocalModelRows(menu, acct.LocalModels)
	} else {
		hideLocalModelRows(menu)
	}
	for i := rows; i < len(menu.balanceItems); i++ {
		setMenuItemVisible(menu.balanceItems[i], &menu.balanceStates[i], false)
		setMenuItemEnabled(menu.balanceItems[i], &menu.balanceStates[i], false)
		menu.balanceActions[i] = ""
	}
	if acct != nil && menu.localModelsItem != nil && len(acct.LocalModels) > 0 {
		return rows + len(acct.LocalModels)
	}
	return rows
}

func showLocalModelRows(menu *providerMenuItems, models []provider.LocalModelUsage) {
	if menu.localModelsItem == nil {
		return
	}
	if len(models) == 0 {
		hideLocalModelRows(menu)
		return
	}
	// A catalog entry with no observations says nothing about available quota.
	// Keep the complete catalog in the detailed CLI/JSON; the tray earns its
	// space by showing activity and dated limit events only.
	observed := make([]provider.LocalModelUsage, 0, len(models))
	unknown := false
	for _, model := range models {
		unknown = unknown || model.AssistantMessages == nil
		used := !model.LimitHitAt.IsZero()
		for _, count := range []*int64{model.AssistantMessages, model.InputTokens, model.OutputTokens, model.ReasoningTokens, model.CacheReadTokens, model.CacheWriteTokens} {
			used = used || (count != nil && *count > 0)
		}
		if used {
			observed = append(observed, model)
		}
	}
	models = observed
	title := fmt.Sprintf("Local activity (%d models)", len(models))
	if len(models) == 1 {
		title = "Local activity (1 model)"
	}
	if len(models) == 0 {
		title = "No local activity · quota unknown"
		if unknown {
			title = "Local activity unavailable · quota unknown"
		}
	}
	setMenuItemTitle(menu.localModelsItem, &menu.localModelsState, title)
	setMenuItemEnabled(menu.localModelsItem, &menu.localModelsState, len(models) > 0)
	setMenuItemVisible(menu.localModelsItem, &menu.localModelsState, true)
	for len(menu.localModelItems) < len(models) {
		item := menu.localModelsItem.AddSubMenuItem("", "")
		item.Disable()
		item.Hide()
		menu.localModelItems = append(menu.localModelItems, item)
		menu.localModelStates = append(menu.localModelStates, menuItemState{visible: false, enabled: false, initialized: true})
	}
	for i, item := range menu.localModelItems {
		if i < len(models) {
			setMenuItemTitle(item, &menu.localModelStates[i], models[i].Summary())
			setMenuItemEnabled(item, &menu.localModelStates[i], false)
			setMenuItemVisible(item, &menu.localModelStates[i], true)
		} else {
			setMenuItemVisible(item, &menu.localModelStates[i], false)
		}
	}
}

func hideLocalModelRows(menu *providerMenuItems) {
	if menu.localModelsItem == nil {
		return
	}
	setMenuItemVisible(menu.localModelsItem, &menu.localModelsState, false)
	for i, item := range menu.localModelItems {
		setMenuItemVisible(item, &menu.localModelStates[i], false)
	}
}

func hideProviderWindows(menu *providerMenuItems) {
	for i, w := range menu.windowItems {
		setMenuItemVisible(w, &menu.windowStates[i], false)
		setWindowTarget(menu, i, iconTarget{})
	}
	setMenuItemVisible(menu.moreItem, &menu.moreState, false)
	for i, b := range menu.balanceItems {
		setMenuItemVisible(b, &menu.balanceStates[i], false)
		setMenuItemEnabled(b, &menu.balanceStates[i], false)
		menu.balanceActions[i] = ""
	}
	hideLocalModelRows(menu)
}

func showProviderHeader(menu *providerMenuItems) {
	if menu.headerAction == "" {
		menu.headerRecovery = recoveryAction{}
	}
	if !menu.connectable {
		setMenuItemEnabled(menu.headerItem, &menu.headerState, menu.opensDashboard)
	}
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
	if menu.headerAction == "" {
		menu.headerRecovery = recoveryAction{}
		s.mu.Lock()
		current := cfg
		s.mu.Unlock()
		menu.headerRecovery = providerRecovery(menu.provider, problem, current, runtime.GOOS)
		problem = format.HumanizeError(problem)
		if menu.provider.Name() == provider.ClaudeAPISourceName && !menu.extensionReady {
			menu.headerRecovery = recoveryAction{label: setupExtensionAction, setupExtension: true}
		}
		if menu.headerRecovery.problem != "" {
			problem = menu.headerRecovery.problem
		}
		problem += " · " + menu.headerRecovery.label
		setMenuItemEnabled(menu.headerItem, &menu.headerState, true)
	}
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
	if menu.connectFailed.Load() && menu.connectProblem != "" {
		s.mu.Lock()
		current := cfg
		s.mu.Unlock()
		menu.headerRecovery = providerRecovery(menu.provider, menu.connectProblem, current, runtime.GOOS)
		menu.headerAction = menu.headerRecovery.label
		menu.headerConnects.Store(false)
		setMenuItemTitle(menu.headerItem, &menu.headerState, menu.displayName+" · "+menu.headerAction)
	}
	setMenuItemEnabled(menu.headerItem, &menu.headerState, true)
}

func finishProviderConnection(menu *providerMenuItems, err error) {
	menu.connectFailed.Store(err != nil)
	menu.connecting.Store(false)
	if err == nil {
		return
	}
	trayRenderMu.Lock()
	menu.connectProblem = err.Error()
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
	// The extension contact is the one piece of I/O the snapshot reads, so it is
	// read before the render lock. The pin decides what the icon and tooltip
	// show, so it is read under the lock: a pin set by a click that takes the
	// lock first must be the one this render draws.
	contact := lastExtensionContact()
	extensionReady := extensionFolderExists()

	results, statuses = lockLatestTrayState(&trayRenderMu)
	defer trayRenderMu.Unlock()
	fetched := results
	results = presentedResults(results)
	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()

	// One snapshot serves the whole render: every row, the icon and the tooltip.
	now := time.Now()
	shown := trayShown(results, fetched, menus, setups)
	snap := view.Build(trayViewInputs(shown, menus, now, contact, currentIconPin()), now)

	hasClaude := false
	displayNames := make(map[string]string, len(menus))
	activeResults := make(map[string]*provider.UsageData, len(results))
	visibleProviderCount := 0
	windowRows := MenuRows(snap, maxWindowItems)
	windowMore := MenuMore(snap, maxWindowItems)

	for name, menu := range menus {
		displayNames[name] = menu.displayName
		menu.extensionReady = extensionReady
		s.mu.Lock()
		menu.sourceRevision = s.lastSourceRevisions[name]
		s.mu.Unlock()
		data, ok := shown[name]
		if !ok {
			hideProviderMenu(menu)
			continue
		}
		acct := accountNamed(snap, name)
		setMenuItemVisible(menu.hideItem, &menu.hideState, hideOffered(name, data, menus, shown, setups))

		setup := setups[name]
		setupReady := setup.IsReady()
		connectionStatus, connectionAction, showConnection := providerConnectionMenuState(name, data, menu.explicitlyEnabled, setupReady)
		if !showConnection {
			menu.connectFailed.Store(false)
		} else if menu.connectFailed.Load() {
			connectionAction = retryConnectionAction
		}
		updateResetCreditRow(menu, data, now)
		updateAnnouncementRow(menu, data, now)
		if provider.FamilyName(menu.provider) == "claude" {
			hasClaude = true
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
			hideProviderWindows(menu)
			expiredMsg := "Token expired"
			if connectionStatus != "" {
				expiredMsg = connectionStatus
			}
			if data.Error != "" {
				if connectionStatus == "" {
					expiredMsg = "Token expired: " + data.Error
				}
			}
			showProviderProblem(menu, expiredMsg)
			showBalanceRows(menu, acct, nil, now)
			setProviderSeparatorVisible(menu, true)
			continue
		}

		if data.Error != "" {
			hideProviderWindows(menu)
			showProviderProblem(menu, data.Error)
			showBalanceRows(menu, acct, nil, now)
			setProviderSeparatorVisible(menu, true)
			continue
		}

		showProviderHeader(menu)
		var statusParts []string
		if len(data.LocalModels) > 0 && len(data.Windows) == 0 {
			statusParts = append(statusParts, "Remaining quota not reported · reset unknown")
		}
		if providerStatus := statuses[provider.FamilyName(menu.provider)]; providerStatus != nil && providerStatus.Indicator.HasIssue() {
			statusParts = append(statusParts, providerStatus.Indicator.Label())
		}
		if data.Stale {
			statusParts = append(statusParts, fmt.Sprintf("Usage unavailable - showing last good data from %s", data.FetchedAt.Local().Format("15:04")))
		}
		menu.statusURL = ""
		if providerStatus := statuses[provider.FamilyName(menu.provider)]; providerStatus != nil && providerStatus.Indicator.HasIssue() {
			menu.statusURL = status.StatusPages[provider.FamilyName(menu.provider)].BaseURL
		}
		if len(statusParts) > 0 {
			if menu.statusURL != "" {
				statusParts = append(statusParts, "Open service status…")
			} else if data.Stale && menu.refreshUsage != nil {
				statusParts = append(statusParts, "Refresh now")
			}
			setMenuItemEnabled(menu.statusItem, &menu.statusState, menu.statusURL != "" || (data.Stale && menu.refreshUsage != nil))
			setMenuItemTitle(menu.statusItem, &menu.statusState, strings.Join(statusParts, " - "))
			setMenuItemVisible(menu.statusItem, &menu.statusState, true)
		} else {
			setMenuItemEnabled(menu.statusItem, &menu.statusState, false)
			setMenuItemVisible(menu.statusItem, &menu.statusState, false)
		}

		choices := manualIconTargets(map[string]*provider.UsageData{name: data})
		windows := acct.ShownWindows()
		rows := windowRows[name]
		for i, window := range windows {
			if i >= len(menu.windowItems) {
				break
			}

			target := iconTarget{Provider: name, Window: window.Name}
			if !targetInChoices(target, choices) {
				target = iconTarget{}
			}
			setWindowTarget(menu, i, target)
			setMenuItemTitle(menu.windowItems[i], &menu.windowStates[i], rows[i])
			// iconPick.update draws the row icon: the risk dot or the pin check.
			menu.windowAtRisk[i] = AtRisk(window)
			setMenuItemVisible(menu.windowItems[i], &menu.windowStates[i], true)

		}

		for i := len(windows); i < len(menu.windowItems); i++ {
			setWindowTarget(menu, i, iconTarget{})
			setMenuItemVisible(menu.windowItems[i], &menu.windowStates[i], false)
		}
		if n := windowMore[name]; n > 0 {
			setMenuItemTitle(menu.moreItem, &menu.moreState, MoreText(n))
			setMenuItemVisible(menu.moreItem, &menu.moreState, true)
		} else {
			setMenuItemVisible(menu.moreItem, &menu.moreState, false)
		}
		balanceRows := showBalanceRows(menu, acct, data.Balances, now)
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

	// Recovery is attached to the affected source header, never a global hint.
	setSharedMenuItemVisible(mReauth, false, &s.reauthVisible, &s.reauthVisibleKnown)
	if mExtension != nil {
		setSharedMenuItemVisible(mExtension, hasClaude && offerExtension(extensionFolderExists(), contact, now),
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

	// Update icon, title and tooltip from the snapshot's selected quota.
	updateTrayIcon(snap)
	updateTrayTitle(activeResults)
	updateTrayTooltip(snap, activeResults)

	// Reset the independent deadline whenever API credit rows are rendered.
	scheduleAPICreditRows(results, menus, now)

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
	menu.resetURL = ""
	if title != "" && provider.FamilyName(menu.provider) == "claude" && data.ResetCredits.Snapshot && !data.ResetCredits.FetchedAt.IsZero() && now.Sub(data.ResetCredits.FetchedAt) > 5*time.Minute {
		menu.resetURL = claudeweb.UsageURL
		title = strings.Replace(title, "open claude.ai signed in to this account", "Open Claude Usage…", 1)
	}
	setMenuItemEnabled(menu.resetItem, &menu.resetState, menu.resetURL != "")
	if title == "" {
		setMenuItemVisible(menu.resetItem, &menu.resetState, false)
		return
	}
	setMenuItemTitle(menu.resetItem, &menu.resetState, title)
	setMenuItemVisible(menu.resetItem, &menu.resetState, true)
}

// updateAnnouncementRow shows the announced Codex reset that bears on this
// account. Attachment already removed announcements that do not apply, so a
// nil announcement hides the row.
func updateAnnouncementRow(menu *providerMenuItems, data *provider.UsageData, now time.Time) {
	var announced *provider.ResetAnnouncement
	if data != nil {
		announced = data.AnnouncedReset
	}
	title := announced.Summary(now)
	if title == "" {
		menu.announceURL = ""
		setMenuItemVisible(menu.announceItem, &menu.announceState, false)
		return
	}
	menu.announceURL = announced.SourceURL
	setMenuItemTitle(menu.announceItem, &menu.announceState, title)
	setMenuItemVisible(menu.announceItem, &menu.announceState, true)
}

// latestFeedReading returns the feed reading the tray renders with.
func latestFeedReading() resetfeed.Reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.feedReading
}

// lastResults returns the last fetched results. Callers must not hold s.mu.
func lastResults() map[string]*provider.UsageData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResults
}

// presentedResults is the one path from stored results to what the tray
// renders: reset-credit folding, then the announced-reset annotation with the
// feed reading current at this render. Every render path calls it, so an
// announcement cannot drop out of the tooltip or the menu on one of them.
// Callers must not hold s.mu.
func presentedResults(results map[string]*provider.UsageData) map[string]*provider.UsageData {
	return resetfeed.AnnounceAll(provider.PresentResetCreditSources(results), latestFeedReading(), time.Now())
}

// refreshResetAnnouncements reads the codex-resets.com feed when the user
// opted in and a Codex source is configured. Otherwise it makes no request.
func refreshResetAnnouncements(current *config.Config, configured []provider.Provider) resetfeed.Reading {
	if !resetfeed.Wanted(current.Settings.CodexResetAnnouncements, configured) {
		return resetfeed.Reading{}
	}
	client, err := resetFeedClient()
	if err != nil {
		log.Printf("codex reset feed: %v", err)
		return resetfeed.Reading{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reading, err := client.Read(ctx)
	if err != nil {
		log.Printf("codex reset feed: %v", err)
	}
	return reading
}

// quotaRecovery finds accounts that have quota again after running out.
var quotaRecovery recoveryWatch

// resetFeedClient is replaced by tests so that no request leaves the process.
var resetFeedClient = resetfeed.Default

// usageRefresher polls the configured providers and redraws the menu.
type usageRefresher struct {
	mu           sync.Mutex // held for the whole of one refresh
	registry     *provider.Registry
	menus        map[string]*providerMenuItems
	reloadConfig func()
	pollSeconds  func() int
	render       func(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus)
	finished     func(at time.Time)
}

// refresh polls now unless another refresh is running; it reports false then.
func (r *usageRefresher) refresh(force bool) bool {
	if !r.mu.TryLock() {
		return false
	}
	defer r.mu.Unlock()
	r.run(force)
	return true
}

// refreshWhenFree polls now, after any refresh that is running ends. That
// refresh read the config before the caller's change, so it cannot stand in.
func (r *usageRefresher) refreshWhenFree(force bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run(force)
}

// run polls and redraws. The caller holds r.mu.
func (r *usageRefresher) run(force bool) {
	r.reloadConfig()

	// Refresh already runs off the tray event loop. Complete executable-path
	// recovery before discovering configured providers so CLI-backed sources
	// cannot disappear based on a startup race.
	shellpath.Init()
	configured := r.registry.GetConfigured()
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

	identities := sourceIdentities(configured, result.SourceRevisions)
	maxGap := 2 * config.PollInterval(r.pollSeconds())
	windowExhaustion.observeFetched(result.Results, skipped, identities, time.Now(), maxGap)
	available := quotaRecovery.observe(result.Results, skipped, identities, time.Now(), maxGap)
	_ = cache.Write(result)

	// The feed is read after the usage cache is written: announcements
	// are display-only and never enter the usage cache.
	s.mu.Lock()
	currentCfg := cfg
	s.mu.Unlock()
	if currentCfg.Settings.NotifyQuotaAvailable && len(available) > 0 {
		if title, body := quotaAvailableNotice(available, result.Results, r.menus, time.Now()); title != "" {
			go notify(title, body, "low")
		}
	}
	feed := refreshResetAnnouncements(currentCfg, configured)
	s.mu.Lock()
	s.feedReading = feed
	s.mu.Unlock()

	now := time.Now()
	s.mu.Lock()
	s.lastResults = result.Results
	s.lastSourceRevisions = result.SourceRevisions
	s.lastRefreshAt = now
	statuses := s.statuses // reuse last known statuses
	s.mu.Unlock()

	r.render(result.Results, statuses)
	r.finished(now)
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

// iconPinPath is replaceable in tests; only the account key and window are saved.
var iconPinPath = func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "clawmeter", "tray-icon-pin.json")
}

func loadIconPin() iconTarget {
	var target iconTarget
	data, err := os.ReadFile(iconPinPath())
	if err != nil || json.Unmarshal(data, &target) != nil || target.Provider == "" || target.Window == "" {
		return iconTarget{}
	}
	return target
}

func saveIconPin(target iconTarget) error {
	path := iconPinPath()
	if path == "" {
		return fmt.Errorf("user cache directory is unavailable")
	}
	if target.Provider == "" {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tray-icon-pin-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// windowTargetsMu guards writes to windowTargets and the reads of row clicks,
// which must not wait for trayRenderMu. Renders write under both locks.
var windowTargetsMu sync.Mutex

// setWindowTarget records exactly what a row renders. The caller holds trayRenderMu.
func setWindowTarget(menu *providerMenuItems, row int, target iconTarget) {
	if menu.windowTargets[row] != target {
		tooltip := ""
		if target.Provider != "" {
			tooltip = "Show this quota in the tray icon"
		}
		menu.windowItems[row].SetTooltip(tooltip)
		windowTargetsMu.Lock()
		menu.windowTargets[row] = target
		windowTargetsMu.Unlock()
	}
	setMenuItemEnabled(menu.windowItems[row], &menu.windowStates[row], target.Provider != "")
}

// setWindowIcon shows a neutral pin on the pinned quota row, otherwise a
// risk dot or nothing. The tray icon itself shows the pinned quota's risk.
// Windows keeps its native check because it has no menu row icons; on KDE
// native checks draw an empty box on every row. The caller holds trayRenderMu.
func setWindowIcon(menu *providerMenuItems, row int, pinned bool) {
	atRisk := menu.windowAtRisk[row]
	key := ""
	switch {
	case pinned:
		key = "pinned"
	case atRisk:
		key = "risk"
	}
	if menu.windowIcons[row] == key {
		return
	}
	menu.windowIcons[row] = key
	if runtime.GOOS == "windows" {
		setMenuItemChecked(menu.windowItems[row], pinned)
	}
	icon := icons.MenuBlank()
	switch key {
	case "pinned":
		icon = icons.MenuPin(resolveTrayPalette())
	case "risk":
		icon = icons.MenuRiskDot()
	}
	setMenuItemIcon(menu.windowItems[row], icon)
}

func (p *iconPicker) forwardRows(menu *providerMenuItems) {
	for row, item := range menu.windowItems {
		go func() {
			for range item.ClickedCh {
				// Read the row's quota now, not after waiting for a render
				// that may give the row another quota.
				windowTargetsMu.Lock()
				target := menu.windowTargets[row]
				windowTargetsMu.Unlock()
				if target.Provider != "" {
					select {
					case p.picked <- target:
					default:
					}
				}
			}
		}()
	}
}

// iconPicker carries quota-row clicks to the main loop, which pins the icon.
type iconPicker struct {
	picked chan iconTarget
}

func newIconPicker() *iconPicker {
	return &iconPicker{picked: make(chan iconTarget, 1)}
}

// update marks the quota row the icon follows. An absent pin displays
// Automatic without forgetting the saved choice, so it applies again when the
// quota returns.
func (p *iconPicker) update(results map[string]*provider.UsageData, menus map[string]*providerMenuItems) {
	s.mu.Lock()
	override := s.iconTargetOverride
	s.mu.Unlock()
	if !targetInChoices(override, manualIconTargets(results)) {
		override = iconTarget{}
	}
	for _, menu := range menus {
		for row, target := range menu.windowTargets {
			setWindowIcon(menu, row, target.Provider != "" && target == override)
		}
	}
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

// selectIconTarget pins the icon to target. Choosing the quota already pinned
// returns the icon to Automatic.
func selectIconTarget(menus map[string]*providerMenuItems, picker *iconPicker, target iconTarget) {
	contact := lastExtensionContact()

	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	s.mu.Lock()
	if s.iconTargetOverride == target {
		target = iconTarget{}
	}
	s.iconTargetOverride = target
	s.mu.Unlock()
	if err := saveIconPin(target); err != nil {
		log.Printf("save tray icon pin: %v", err)
	}

	systray.BeginMenuUpdate()
	defer systray.EndMenuUpdate()
	picker.update(presentedResults(lastResults()), menus)
	renderIconAndTooltip(menus, contact)
}

// renderIconAndTooltip redraws the icon, title and tooltip from one snapshot of
// the last results. contact is the extension contact, read before the caller
// took the render lock. The caller holds trayRenderMu.
func renderIconAndTooltip(menus map[string]*providerMenuItems, contact time.Time) {
	raw := lastResults()
	results := presentedResults(raw)
	shown := trayShown(results, raw, menus, nil)
	now := time.Now()
	snap := view.Build(trayViewInputs(shown, menus, now, contact, currentIconPin()), now)
	updateTrayIcon(snap)
	updateTrayTitle(results)
	updateTrayTooltip(snap, shown)
}

// trayShown decides which menus the tray shows and returns each shown menu's
// reading, nil included. A provider folded under another row is not shown.
// Like the CLI, an auto-detected provider shows only with something to show;
// the Token Plan also shows when it can be connected. It also records that a
// provider has been healthy, which keeps it visible through a later failure.
func trayShown(results, fetched map[string]*provider.UsageData, menus map[string]*providerMenuItems, setups map[string]provider.SetupStatus) map[string]*provider.UsageData {
	shown := make(map[string]*provider.UsageData, len(menus))
	for name, menu := range menus {
		data := results[name]
		if menu.disabled || data == nil && fetched[name] != nil {
			continue
		}
		if data != nil && data.EstablishesPrimaryUIHistory() {
			menu.everHealthy = true
		}
		setup := setups[name]
		detected := name == tokenPlanProviderName && setup.State != provider.SetupUnavailable
		if !provider.ShouldShowInPrimaryUI(data, menu.everHealthy, menu.explicitlyEnabled) && !detected {
			continue
		}
		shown[name] = data
	}
	return shown
}

// trayViewInputs is what the snapshot reads for one render. Its inputs are the
// shown readings, the menus' names, labels and families, every configured
// source of a family for the labels, the run-out record, the extension contact
// and the user's pin.
func trayViewInputs(shown map[string]*provider.UsageData, menus map[string]*providerMenuItems, now, contact time.Time, pin *view.Target) view.Inputs {
	s.mu.Lock()
	pollSeconds := 0
	if cfg != nil {
		pollSeconds = cfg.Settings.PollInterval
	}
	s.mu.Unlock()
	in := view.Inputs{
		MaxReadingAge:      config.ReadingMaxAge(pollSeconds),
		Results:            make(map[string]*provider.UsageData, len(shown)),
		DisplayNames:       make(map[string]string, len(menus)),
		SourceLabels:       make(map[string]string, len(menus)),
		Families:           make(map[string]string, len(menus)),
		FamilySourceCounts: make(map[string]int),
		OutSince:           make(map[string]map[string]time.Time),
		ExtensionContact:   contact,
	}
	// A saved pin applies only while its quota can be pinned, as the rows
	// show; otherwise the icon follows Automatic.
	if pin != nil && targetInChoices(iconTarget{Provider: pin.AccountKey, Window: pin.Window}, manualIconTargets(shown)) {
		in.Pin = pin
	}
	for key, menu := range menus {
		family := provider.FamilyName(menu.provider)
		in.DisplayNames[key] = menu.provider.DisplayName()
		in.SourceLabels[key] = provider.SourceLabel(menu.provider)
		in.Families[key] = family
		in.FamilySourceCounts[family]++
	}
	for key, data := range shown {
		in.Results[key] = data
		if data == nil {
			continue
		}
		for _, w := range data.PresentationWindows() {
			if w.Utilization < 100 {
				continue
			}
			if at := windowExhaustion.ranOutAt(key, w, now); !at.IsZero() {
				if in.OutSince[key] == nil {
					in.OutSince[key] = make(map[string]time.Time)
				}
				in.OutSince[key][w.Name] = at
			}
		}
	}
	return in
}

// currentIconPin is the quota the user pinned to the icon, or nil when the
// icon is automatic.
func currentIconPin() *view.Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.iconTargetOverride.Provider == "" {
		return nil
	}
	return &view.Target{AccountKey: s.iconTargetOverride.Provider, Window: s.iconTargetOverride.Window}
}

// manualIconTargets lists the quotas a user can pin: each reset-backed window
// of each source, in source key order.
func manualIconTargets(results map[string]*provider.UsageData) []iconTarget {
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)
	var choices []iconTarget
	for _, name := range names {
		for _, window := range results[name].UsableWindows() {
			choices = append(choices, iconTarget{Provider: name, Window: window.Name})
		}
	}
	return choices
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

func providerDisplayName(name string, displayNames map[string]string) string {
	if displayNames != nil && displayNames[name] != "" {
		return displayNames[name]
	}
	if name == "" {
		return "clawmeter"
	}
	return strings.ToUpper(name[:1]) + name[1:]
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

func updateTrayIcon(snap view.Snapshot) {
	// The icon follows the snapshot's selected quota: the user's pin when set
	// and present, else the most at-risk one. Setup and fetch errors are in the
	// menu; they never steal the icon from quota telemetry.
	worstProvider, meter := IconState(snap)
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

func updateTrayTooltip(snap view.Snapshot, data map[string]*provider.UsageData) {
	version := ""
	if rel := currentPendingRelease(); rel != nil {
		version = rel.Version
	}
	tooltip := AppendUpdateLine(Tooltip(snap, data), version)
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

// sendThresholdNotification delivers off the caller's goroutine: thresholds
// are checked under the render lock, and a slow notification service must not
// hold up the menu. Tests replace it to capture notices.
var sendThresholdNotification = func(title, body, urgency string) {
	go notify(title, body, urgency)
}

func checkThresholds(results map[string]*provider.UsageData, displayNames map[string]string) {
	type notification struct{ title, body, urgency string }
	var pending []notification
	now := time.Now()
	s.mu.Lock()
	if s.thresholdUtilization == nil {
		s.thresholdUtilization = make(map[iconTarget]thresholdSample)
	}
	warningThreshold, criticalThreshold := 80.0, 95.0
	pollSeconds := 0
	if cfg != nil {
		pollSeconds = cfg.Settings.PollInterval
		warningThreshold = cfg.Settings.NotificationThresholds.Warning
		criticalThreshold = cfg.Settings.NotificationThresholds.Critical
	}
	snap := view.Build(view.Inputs{Results: results, MaxReadingAge: config.ReadingMaxAge(pollSeconds)}, now)
	for name, data := range results {
		if data == nil || data.Error != "" || data.Stale || data.IsExpired {
			continue
		}
		for _, window := range accountNamed(snap, name).Windows {
			if !window.Reset.Known || !window.Reset.At.After(now) {
				continue // A passed reset is stale data, not a new threshold crossing.
			}
			key := iconTarget{Provider: name, Window: window.Name}
			previous := s.thresholdUtilization[key]
			pct, oldPct := window.Used, previous.Utilization
			// A new reset period rearms thresholds even if low samples were missed.
			if newResetPeriod(previous.ResetsAt, window.Reset.At, now) {
				oldPct = 0
			}
			s.thresholdUtilization[key] = thresholdSample{Utilization: pct, ResetsAt: window.Reset.At}
			display := displayNames[name]
			if display == "" {
				display = name
			}
			words := window.ForecastWords()
			if note := window.ExhaustionWords(); note != "" {
				words += " · " + note
			}
			message := fmt.Sprintf("%s window at %.0f%% — %s", window.Name, pct, words)
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
	setIconByName("logo-off", icons.AppOff)
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
	if err := browser.OpenURL(url); err != nil {
		notify("Clawmeter", "Could not open the page: "+err.Error(), "normal")
	}
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

// cachedResultsForCurrentSources returns the cached readings that belong to
// each provider's current login, and the revisions they were accepted under.
func cachedResultsForCurrentSources(entry *cache.Entry, providers []provider.Provider) (map[string]*provider.UsageData, map[string]string) {
	current := providerSourceRevisions(providers)
	results := make(map[string]*provider.UsageData)
	accepted := make(map[string]string)
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
			if revision := current[name]; revision != "" {
				accepted[name] = revision
			}
		}
	}
	return results, accepted
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

// nextAPICreditDeadline repaints expiring grants and newly stale API pools.
func nextAPICreditDeadline(results map[string]*provider.UsageData, now time.Time) time.Time {
	var earliest time.Time
	for _, data := range results {
		if data == nil {
			continue
		}
		for _, grant := range data.CreditGrants {
			if grant.ExpiresAt.After(now) && (earliest.IsZero() || grant.ExpiresAt.Before(earliest)) {
				earliest = grant.ExpiresAt
			}
		}
		for _, benefit := range data.CreditBenefits {
			for _, deadline := range []time.Time{benefit.ObservedAt.Add(5 * time.Minute), benefit.LinkObservedAt.Add(5 * time.Minute), benefit.ExpiresAt} {
				if deadline.After(now) && (earliest.IsZero() || deadline.Before(earliest)) {
					earliest = deadline
				}
			}
		}
		for _, balance := range data.APICreditBalances {
			deadlines := []time.Time{balance.ObservedAt.Add(5 * time.Minute)}
			for _, grant := range balance.Grants {
				if grant.Remaining > 0 {
					deadlines = append(deadlines, grant.ExpiresAt)
				}
			}
			for _, deadline := range deadlines {
				if deadline.After(now) && (earliest.IsZero() || deadline.Before(earliest)) {
					earliest = deadline
				}
			}
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
		contact := lastExtensionContact()
		latest, _ := lockLatestTrayState(&trayRenderMu)
		defer trayRenderMu.Unlock()
		if generation != apiCreditsTimerGeneration {
			return
		}
		results := presentedResults(latest)
		systray.BeginMenuUpdate()
		defer systray.EndMenuUpdate()
		now := time.Now()
		shown := trayShown(results, latest, menus, nil)
		snap := view.Build(trayViewInputs(shown, menus, now, contact, currentIconPin()), now)
		for name, menu := range menus {
			data := results[name]
			if !menu.headerState.visible || data == nil || (len(data.CreditPools) == 0 && len(data.CreditGrants) == 0) {
				continue
			}
			balances := data.Balances
			if data.IsExpired || data.Error != "" {
				balances = nil
			}
			showBalanceRows(menu, accountNamed(snap, name), balances, now)
		}
		scheduleAPICreditRows(results, menus, time.Now())
	})
}
