//go:build tray

package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

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
	updateCheckInterval = 30 * time.Minute
)

type state struct {
	mu                   sync.Mutex
	lastResults          map[string]*provider.UsageData
	statuses             map[string]*status.ProviderStatus
	failureGate          *provider.FailureGate
	currentTitle         string
	currentTooltip       string
	lastRefreshAt        time.Time
	iconProviderOverride string
	iconProviderChoices  []string
}

var (
	s       state
	version string
	cfg     *config.Config
)

func Run(ver string) int {
	version = ver
	shellpath.Init()
	setupIconTheme()
	systray.Run(onReady, func() {
		cleanupIconTheme()
	})
	return 0
}

func onReady() {
	s.failureGate = provider.NewFailureGate()

	var err error
	cfg, err = config.Load()
	if err != nil {
		setErrorState("Config error")
		return
	}

	// Launch at login is opt-in: the user enables it from the tray menu.
	// Do not silently persist anything on first run.

	setIconByName("gray", icons.Gray)
	systray.SetTitle("clawmeter")
	systray.SetTooltip("Clawmeter — loading...")

	// Build header
	mHeader := systray.AddMenuItem(fmt.Sprintf("Clawmeter %s", version), "")
	mHeader.Disable()
	systray.AddSeparator()

	// Create registry and register providers
	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	// Provider menu items - dynamically created based on configured providers
	providerMenus := make(map[string]*providerMenuItems)

	for _, p := range registry.GetConfigured() {
		explicit := cfg.IsProviderExplicitlyEnabled(p.Name())
		menu := createProviderMenuItems(p, explicit)
		if !explicit {
			hideProviderMenu(menu)
		}
		providerMenus[p.Name()] = menu
	}

	if len(providerMenus) == 0 {
		systray.AddMenuItem("No active providers", "").Disable()
	}

	systray.AddSeparator()

	// Global menu items
	mIconProvider := systray.AddMenuItem("Icon: Auto", "")
	mRefresh := systray.AddMenuItem("Refresh Now", "")
	systray.AddSeparator()

	mReauth := systray.AddMenuItem("Run `claude` to reauth", "")
	mReauth.Hide()

	var pendingRelease *update.Release
	var pendingReleaseMu sync.Mutex
	var updateChecking sync.Mutex
	mUpdate := systray.AddMenuItem("", "")
	mUpdate.Hide()

	systray.AddSeparator()
	mAutostart := systray.AddMenuItem("", "")
	updateAutostartLabel(mAutostart)
	mQuit := systray.AddMenuItem("Quit", "")

	// Guard against concurrent refreshes
	var refreshing sync.Mutex

	// Collect configured provider names once
	configuredNames := providerNames(registry.GetConfigured())

	// Refresh usage only (lightweight, runs every poll cycle)
	refresh := func(force bool) {
		if !refreshing.TryLock() {
			return // already refreshing
		}
		defer refreshing.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Split providers: fetch active ones, use cache for backed-off ones
		s.mu.Lock()
		var toFetch []provider.Provider
		skipped := make(map[string]*provider.UsageData)
		for _, p := range registry.GetConfigured() {
			if !force && s.failureGate.InBackoff(p.Name()) {
				if prev, ok := s.lastResults[p.Name()]; ok && prev != nil {
					skipped[p.Name()] = prev.Clone()
				}
			} else {
				toFetch = append(toFetch, p)
			}
		}
		s.mu.Unlock()

		result := provider.FetchProvidersParallel(ctx, toFetch)

		// Merge in cached data for backed-off providers
		for name, cached := range skipped {
			result.Results[name] = cached
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
			if data.Error == "" && !data.IsExpired {
				s.failureGate.RecordSuccess(name)
				continue
			}
			// Provider errored — check if we should suppress it
			hasPrior := false
			if prev, ok := s.lastResults[name]; ok && prev != nil && prev.IsHealthy() {
				hasPrior = true
			}
			if hasPrior && len(data.Windows) == 0 && provider.IsTransientFetchError(data.Error) {
				// Codex/OpenAI transport blip — keep showing the last good windows.
				if prev, ok := s.lastResults[name]; ok && prev != nil {
					result.Results[name] = prev.Clone()
				}
				_ = s.failureGate.ShouldSurfaceError(name, true)
			} else if !s.failureGate.ShouldSurfaceError(name, hasPrior) {
				// First failure with prior data — keep showing cache silently.
				if prev, ok := s.lastResults[name]; ok && prev != nil {
					result.Results[name] = prev.Clone()
				}
			} else if hasPrior && len(data.Windows) == 0 {
				// Persistent failure — fall back to cache but show the error
				prev := s.lastResults[name].Clone()
				prev.Error = data.Error + " (showing cached)"
				result.Results[name] = prev
			}
		}
		s.mu.Unlock()

		_ = cache.Write(result)

		now := time.Now()
		s.mu.Lock()
		s.lastResults = result.Results
		s.lastRefreshAt = now
		statuses := s.statuses // reuse last known statuses
		s.mu.Unlock()

		updateUI(result.Results, statuses, providerMenus, mReauth, mIconProvider)
		mRefresh.SetTitle(fmt.Sprintf("Refresh Now  (updated %s)", now.Format("15:04")))
	}

	// Refresh status pages (heavier, runs less often)
	refreshStatus := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		statuses := status.FetchAll(ctx, configuredNames)

		s.mu.Lock()
		s.statuses = statuses
		lastResults := s.lastResults
		s.mu.Unlock()

		if lastResults != nil {
			updateUI(lastResults, statuses, providerMenus, mReauth, mIconProvider)
		}
	}

	// Check update function
	checkUpdate := func() {
		if !updateChecking.TryLock() {
			return
		}
		defer updateChecking.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rel, err := update.Check(ctx, version)
		if err != nil || rel == nil {
			return
		}
		pendingReleaseMu.Lock()
		pendingRelease = rel
		pendingReleaseMu.Unlock()
		mUpdate.SetTitle(fmt.Sprintf("Update to %s", rel.Version))
		mUpdate.Show()
	}

	// Apply update function
	applyUpdate := func() {
		pendingReleaseMu.Lock()
		rel := pendingRelease
		pendingReleaseMu.Unlock()
		if rel == nil {
			return
		}
		mUpdate.SetTitle("Updating...")
		mUpdate.Disable()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		exe, err := update.ExecutablePath()
		if err != nil {
			mUpdate.SetTitle(fmt.Sprintf("Update failed: %v", err))
			mUpdate.Enable()
			return
		}
		if err := update.ApplyTo(ctx, rel.URL, exe); err != nil {
			mUpdate.SetTitle(fmt.Sprintf("Update failed: %v", err))
			mUpdate.Enable()
			return
		}
		notify("Clawmeter", fmt.Sprintf("Updated to %s — restarting", rel.Version), "low")
		if err := update.Restart(exe); err != nil {
			mUpdate.SetTitle("Updated — restart Clawmeter")
			mUpdate.Enable()
			notify("Clawmeter", "Updated, but restart failed. Restart Clawmeter manually.", "normal")
			return
		}
		systray.Quit()
	}

	// Show cached data immediately while fetching fresh data
	if cached, err := cache.Read(); err == nil && cached != nil {
		filtered := provider.FilterUsageDataByNames(cached.ProviderData, configuredNames)
		s.mu.Lock()
		s.lastResults = filtered
		s.lastRefreshAt = cached.FetchedAt
		s.mu.Unlock()
		updateUI(filtered, nil, providerMenus, mReauth, mIconProvider)
	}

	// Initial refresh in background (don't block tray UI)
	go refresh(false)
	go refreshStatus()

	// Check for updates on startup
	go checkUpdate()

	// Setup tickers
	pollInterval := time.Duration(cfg.Settings.PollInterval) * time.Second
	if pollInterval < 60*time.Second {
		pollInterval = 5 * time.Minute // minimum 5 minutes
	}
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
			case <-mIconProvider.ClickedCh:
				cycleIconProviderOverride()
				s.mu.Lock()
				lastResults := s.lastResults
				statuses := s.statuses
				s.mu.Unlock()
				updateUI(lastResults, statuses, providerMenus, mReauth, mIconProvider)
			case <-mUpdate.ClickedCh:
				applyUpdate()
			case <-mReauth.ClickedCh:
				openTerminalWithClaude()
			case <-mAutostart.ClickedCh:
				toggleAutostart(mAutostart)
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

// providerMenuItems holds menu items for a single provider.
type providerMenuItems struct {
	provider          provider.Provider
	headerItem        *systray.MenuItem
	statusItem        *systray.MenuItem
	windowItems       []*systray.MenuItem
	dashboardItem     *systray.MenuItem
	everHealthy       bool // true once we've seen useful quota data
	explicitlyEnabled bool
}

const maxWindowItems = 8 // pre-allocate up to 8 window slots per provider

func createProviderMenuItems(p provider.Provider, explicitlyEnabled bool) *providerMenuItems {
	// Provider header (disabled)
	header := systray.AddMenuItem(p.DisplayName(), "")
	header.Disable()

	// Status item (shows loading/error)
	statusItem := systray.AddMenuItem("Loading...", "")
	statusItem.Disable()

	// Pre-create window items (hidden until populated)
	windowItems := make([]*systray.MenuItem, maxWindowItems)
	for i := range windowItems {
		item := systray.AddMenuItem("", "")
		item.Disable()
		item.Hide()
		windowItems[i] = item
	}

	// Dashboard item (clickable)
	dashboardItem := systray.AddMenuItem(fmt.Sprintf("Open %s Dashboard", p.DisplayName()), p.DashboardURL())
	go func() {
		for range dashboardItem.ClickedCh {
			openURL(p.DashboardURL())
		}
	}()

	systray.AddSeparator()

	return &providerMenuItems{
		provider:          p,
		headerItem:        header,
		statusItem:        statusItem,
		windowItems:       windowItems,
		dashboardItem:     dashboardItem,
		explicitlyEnabled: explicitlyEnabled,
	}
}

func hideProviderMenu(menu *providerMenuItems) {
	menu.headerItem.Hide()
	menu.statusItem.Hide()
	hideProviderWindows(menu)
	menu.dashboardItem.Hide()
}

func hideProviderWindows(menu *providerMenuItems) {
	for _, w := range menu.windowItems {
		w.Hide()
	}
}

func showProviderHeader(menu *providerMenuItems) {
	menu.headerItem.Show()
}

func updateUI(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus, menus map[string]*providerMenuItems, mReauth *systray.MenuItem, mIconProvider *systray.MenuItem) {
	hasExpiredClaude := false
	displayNames := make(map[string]string, len(menus))
	activeResults := make(map[string]*provider.UsageData, len(results))

	for name, menu := range menus {
		displayNames[name] = menu.provider.DisplayName()
		data := results[name]

		if data != nil && data.EstablishesPrimaryUIHistory() {
			menu.everHealthy = true
		}

		if !provider.ShouldShowInPrimaryUI(data, menu.everHealthy, menu.explicitlyEnabled) {
			hideProviderMenu(menu)
			continue
		}
		if data != nil {
			activeResults[name] = data
		}

		if data == nil {
			showProviderHeader(menu)
			menu.statusItem.SetTitle("No data")
			menu.statusItem.Show()
			hideProviderWindows(menu)
			menu.dashboardItem.Show()
			continue
		}

		if data.IsExpired {
			if name == "claude" {
				hasExpiredClaude = true
			}
			showProviderHeader(menu)
			hideProviderWindows(menu)
			expiredMsg := "Token expired"
			if data.Error != "" {
				expiredMsg = format.HumanizeError(data.Error)
			}
			menu.statusItem.SetTitle(expiredMsg)
			menu.statusItem.Show()
			menu.dashboardItem.Show()
			continue
		}

		if data.Error != "" {
			showProviderHeader(menu)
			hideProviderWindows(menu)
			menu.statusItem.SetTitle(format.HumanizeError(data.Error))
			menu.statusItem.Show()
			menu.dashboardItem.Show()
			continue
		}

		// Hide status, show window items
		showProviderHeader(menu)
		menu.statusItem.Hide()

		for i, window := range data.Windows {
			if i >= len(menu.windowItems) {
				break
			}

			proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
			resetStr := format.FormatDuration(time.Until(window.ResetsAt))
			menu.windowItems[i].SetTitle(fmt.Sprintf("%s: %.0f%% — %s — %s",
				window.Name, window.Utilization, resetStr, proj.PaceIndicator()))
			menu.windowItems[i].Show()

		}

		for i := len(data.Windows); i < len(menu.windowItems); i++ {
			menu.windowItems[i].Hide()
		}

		menu.dashboardItem.Show()
	}

	// Show/hide reauth button (only for Claude expired tokens)
	if hasExpiredClaude {
		mReauth.Show()
	} else {
		mReauth.Hide()
	}

	updateIconProviderSelector(activeResults, displayNames, mIconProvider)

	// Update icon and title based on worst usage (only active providers)
	updateTrayIcon(activeResults)
	updateTrayTitle(activeResults)
	updateTrayTooltip(activeResults, statuses, displayNames)

	// Check notification thresholds
	checkThresholds(activeResults, displayNames)
}

func updateIconProviderSelector(results map[string]*provider.UsageData, displayNames map[string]string, item *systray.MenuItem) {
	if item == nil {
		return
	}
	choices := activeIconProviderChoices(results)

	s.mu.Lock()
	if s.iconProviderOverride != "" && !containsString(choices, s.iconProviderOverride) {
		s.iconProviderOverride = ""
	}
	s.iconProviderChoices = choices
	override := s.iconProviderOverride
	s.mu.Unlock()

	if len(choices) == 0 {
		item.SetTitle("Icon: Auto")
		item.Disable()
		return
	}

	item.Enable()
	if override == "" {
		item.SetTitle("Icon: Auto")
		return
	}
	item.SetTitle("Icon: " + providerDisplayName(override, displayNames))
}

func cycleIconProviderOverride() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.iconProviderOverride = nextIconProviderOverride(s.iconProviderOverride, s.iconProviderChoices)
}

func nextIconProviderOverride(current string, choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	if current == "" {
		return choices[0]
	}
	for i, choice := range choices {
		if choice != current {
			continue
		}
		if i == len(choices)-1 {
			return ""
		}
		return choices[i+1]
	}
	return ""
}

func activeIconProviderChoices(results map[string]*provider.UsageData) []string {
	choices := make([]string, 0, len(results))
	for name, data := range results {
		if data != nil {
			choices = append(choices, name)
		}
	}
	sort.Strings(choices)
	return choices
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
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

func selectedTrayProvider(results map[string]*provider.UsageData) (string, *provider.UsageData, bool) {
	s.mu.Lock()
	override := s.iconProviderOverride
	s.mu.Unlock()

	if override != "" {
		if data := results[override]; data != nil {
			return override, data, true
		}
	}

	for _, name := range sortedKeys(results) {
		data := results[name]
		if data != nil {
			return name, data, true
		}
	}
	return "", nil, false
}

func updateTrayIcon(results map[string]*provider.UsageData) {
	// Pick the icon's provider using the same severity ordering as the title
	// and tooltip so an expired/error state never hides behind a healthier
	// provider's icon. Expired/error map to a red full meter even though
	// there's no real utilization number.
	worstProvider := ""
	meter := icons.MeterState{}

	if name, data, ok := selectedTrayProvider(results); ok {
		meter = iconMeterState(data)
		worstProvider = name
	}

	iconData := icons.GenerateProviderIconWithMeter(worstProvider, meter, 128)
	setIconDynamic(worstProvider, meter, iconData)
}

// iconMeterState maps provider usage into the tray icon's three visual
// channels: actual usage, expected usage by this point in the reset window, and
// projected risk. The popup keeps exact text; the icon carries the comparison.
func iconMeterState(data *provider.UsageData) icons.MeterState {
	if data == nil {
		return icons.MeterState{}
	}
	if data.IsExpired {
		return icons.MeterState{UsagePct: 100, ExpectedPct: 100, RiskPct: 100}
	}
	if data.Error != "" && len(data.Windows) == 0 {
		return icons.MeterState{UsagePct: 100, ExpectedPct: 100, RiskPct: 100}
	}

	var state icons.MeterState
	worstRisk := -1.0
	for _, window := range data.Windows {
		windowLen := forecast.GuessWindowType(window.Name)
		proj := forecast.Project(window.Utilization, window.ResetsAt, windowLen)
		if proj.ProjectedPct > worstRisk {
			worstRisk = proj.ProjectedPct
			state = icons.MeterState{
				UsagePct:     window.Utilization,
				ExpectedPct:  expectedUsagePct(window.ResetsAt, windowLen),
				RiskPct:      proj.ProjectedPct,
				ShowExpected: true,
			}
		}
	}
	return state
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
	var title string
	name, data, ok := selectedTrayProvider(results)
	if ok {
		display := providerDisplayName(name, nil)

		if data.IsExpired {
			title = display + " expired"
		} else if data.Error != "" && len(data.Windows) == 0 {
			title = display + " error"
		} else {
			title = fmt.Sprintf("%s %.0f%%", display, providerProjectedPct(data))
		}
	}
	if title == "" {
		title = "clawmeter"
	}

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

func providerProjectedPct(data *provider.UsageData) float64 {
	if data == nil {
		return 0
	}
	worst := 0.0
	for _, window := range data.Windows {
		proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
		if proj.ProjectedPct > worst {
			worst = proj.ProjectedPct
		}
	}
	return worst
}

func updateTrayTooltip(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus, displayNames map[string]string) {
	var blocks []string

	for _, name := range sortedKeys(results) {
		data := results[name]
		if data == nil {
			continue
		}

		display := displayNames[name]
		if display == "" {
			display = name
		}

		if data.IsExpired {
			msg := data.Error
			if msg == "" {
				msg = "token expired"
			}
			blocks = append(blocks, fmt.Sprintf("%s: %s", display, format.HumanizeError(msg)))
			continue
		}

		if data.Error != "" && len(data.Windows) == 0 {
			blocks = append(blocks, fmt.Sprintf("%s: error — %s", display, format.HumanizeError(data.Error)))
			continue
		}

		var windowLines []string
		for _, window := range data.Windows {
			remaining := time.Until(window.ResetsAt)
			proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
			var line string
			if remaining <= 0 {
				line = fmt.Sprintf("  %s: %.0f%%  just reset", window.Name, window.Utilization)
			} else {
				line = fmt.Sprintf("  %s: %.0f%%  resets %s", window.Name, window.Utilization, format.FormatDuration(remaining))
				if window.Utilization > 0 {
					line += "  " + proj.PaceIndicator()
				}
			}
			windowLines = append(windowLines, line)
		}

		block := display + "\n" + strings.Join(windowLines, "\n")
		blocks = append(blocks, block)
	}

	var tooltip string
	if len(blocks) > 0 {
		tooltip = strings.Join(blocks, "\n\n")
		s.mu.Lock()
		refreshTime := s.lastRefreshAt
		s.mu.Unlock()
		if !refreshTime.IsZero() {
			tooltip += "\n\nUpdated " + format.FormatDuration(time.Since(refreshTime)) + " ago"
		}
	} else {
		tooltip = "Clawmeter — no active providers"
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
		if data == nil || data.Error != "" {
			continue
		}

		oldData, hadOld := s.lastResults[name]

		for _, window := range data.Windows {
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

			if pct >= criticalThreshold && oldPct < criticalThreshold {
				notify(fmt.Sprintf("%s usage critical", display),
					fmt.Sprintf("%s window at %.0f%% — rate limiting likely before reset", window.Name, pct),
					"critical")
			} else if pct >= warningThreshold && oldPct < warningThreshold {
				notify(fmt.Sprintf("%s usage warning", display),
					fmt.Sprintf("%s window at %.0f%% — on pace to reach limit", window.Name, pct),
					"normal")
			}
		}
	}

	// Update stored results
	s.lastResults = results
}

func setErrorState(msg string) {
	setIconByName("gray", icons.Gray)
	systray.SetTitle("error")
	systray.SetTooltip(fmt.Sprintf("Clawmeter — %s", msg))
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
		updateAutostartLabel(m)
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
	updateAutostartLabel(m)
}

func openURL(url string) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdg-open", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	}
}

func openTerminalWithClaude() {
	switch runtime.GOOS {
	case "linux":
		for _, term := range []string{"konsole", "gnome-terminal", "xterm"} {
			if path, err := exec.LookPath(term); err == nil {
				exec.Command(path, "-e", "claude").Start()
				return
			}
		}
	case "darwin":
		exec.Command("open", "-a", "Terminal", "claude").Start()
	}
}

func notify(title, body, urgency string) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("notify-send", "-u", urgency, title, body).Run()
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		exec.Command("osascript", "-e", script).Run()
	}
}

func providerNames(providers []provider.Provider) []string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	return names
}

// sortedKeys returns provider names sorted by severity (worst first).
// Expired > error > highest projected usage > alphabetical.
func sortedKeys(m map[string]*provider.UsageData) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		di, dj := m[keys[i]], m[keys[j]]
		si, sj := providerSeverity(di), providerSeverity(dj)
		if si != sj {
			return si > sj // higher severity first
		}
		return keys[i] < keys[j] // alphabetical tiebreak
	})
	return keys
}

// providerSeverity returns a numeric severity score for sorting.
// Higher = more urgent.
func providerSeverity(data *provider.UsageData) float64 {
	if data == nil {
		return -1
	}
	if data.IsExpired {
		return 10000 // expired always on top
	}
	if data.Error != "" && len(data.Windows) == 0 {
		return 9000 // error next
	}
	return providerProjectedPct(data)
}
