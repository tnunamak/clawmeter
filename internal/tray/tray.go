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
	"github.com/tnunamak/clawmeter/internal/status"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
	"github.com/tnunamak/clawmeter/internal/update"
)

const (
	updateCheckInterval = 4 * time.Hour
)

type state struct {
	mu             sync.Mutex
	lastResults    map[string]*provider.UsageData
	statuses       map[string]*status.ProviderStatus
	failureGate    *provider.FailureGate
	currentTitle   string
	currentTooltip string
	lastRefreshAt  time.Time
}

var (
	s       state
	version string
	cfg     *config.Config
)

func Run(ver string) int {
	version = ver
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

	// Auto-enable launch at login on first run
	if !autostart.IsInstalled() {
		if err := autostart.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: warning: failed to enable autostart: %v\n", err)
		} else {
			notify("Clawmeter", "Enabled launch at login. Disable from the tray menu.", "low")
		}
	}

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

	for _, p := range registry.GetAll() {
		if !p.IsConfigured() {
			continue
		}
		providerMenus[p.Name()] = createProviderMenuItems(p)
	}

	if len(providerMenus) == 0 {
		systray.AddMenuItem("No providers configured", "").Disable()
	}

	systray.AddSeparator()

	// Global menu items
	mRefresh := systray.AddMenuItem("Refresh Now", "")
	systray.AddSeparator()

	mReauth := systray.AddMenuItem("Run `claude` to reauth", "")
	mReauth.Hide()

	var pendingRelease *update.Release
	mUpdate := systray.AddMenuItem("", "")
	mUpdate.Hide()

	systray.AddSeparator()
	mAutostart := systray.AddMenuItem("", "")
	updateAutostartLabel(mAutostart)
	mQuit := systray.AddMenuItem("Quit", "")

	// Guard against concurrent refreshes
	var refreshing sync.Mutex

	// Collect configured provider names once
	var configuredNames []string
	for _, p := range registry.GetConfigured() {
		configuredNames = append(configuredNames, p.Name())
	}

	// Refresh usage only (lightweight, runs every poll cycle)
	refresh := func() {
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
			if s.failureGate.InBackoff(p.Name()) {
				if prev, ok := s.lastResults[p.Name()]; ok && prev != nil {
					skipped[p.Name()] = prev
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
			if !s.failureGate.ShouldSurfaceError(name, hasPrior) {
				// First transient failure with prior data — keep showing cache silently
				if prev, ok := s.lastResults[name]; ok && prev != nil {
					result.Results[name] = prev
				}
			} else if hasPrior && len(data.Windows) == 0 {
				// Persistent failure — fall back to cache but show the error
				prev := s.lastResults[name]
				prev.Error = data.Error + " (showing cached)"
				result.Results[name] = prev
			}
		}
		s.mu.Unlock()

		_ = cache.Write(result)

		s.mu.Lock()
		s.lastResults = result.Results
		s.lastRefreshAt = time.Now()
		statuses := s.statuses // reuse last known statuses
		s.mu.Unlock()

		updateUI(result.Results, statuses, providerMenus, mReauth)
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
			updateUI(lastResults, statuses, providerMenus, mReauth)
		}
	}

	// Check update function
	checkUpdate := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rel, err := update.Check(ctx, version)
		if err != nil || rel == nil {
			return
		}
		pendingRelease = rel
		mUpdate.SetTitle(fmt.Sprintf("Update to %s", rel.Version))
		mUpdate.Show()
	}

	// Apply update function
	applyUpdate := func() {
		if pendingRelease == nil {
			return
		}
		mUpdate.SetTitle("Updating...")
		mUpdate.Disable()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := update.Apply(ctx, pendingRelease.URL); err != nil {
			mUpdate.SetTitle(fmt.Sprintf("Update failed: %v", err))
			mUpdate.Enable()
			return
		}
		notify("Clawmeter", fmt.Sprintf("Updated to %s — restarting", pendingRelease.Version), "low")
		update.Restart()
		systray.Quit()
	}

	// Show cached data immediately while fetching fresh data
	if cached, err := cache.Read(); err == nil && cached != nil {
		s.mu.Lock()
		s.lastResults = cached.ProviderData
		s.lastRefreshAt = cached.FetchedAt
		s.mu.Unlock()
		updateUI(cached.ProviderData, nil, providerMenus, mReauth)
	}

	// Initial refresh in background (don't block tray UI)
	go refresh()
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
				go refresh()
			case <-statusTicker.C:
				go refreshStatus()
			case <-updateTicker.C:
				go checkUpdate()
			case <-mRefresh.ClickedCh:
				go refresh()
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
	provider      provider.Provider
	statusItem    *systray.MenuItem
	windowItems   []*systray.MenuItem
	dashboardItem *systray.MenuItem
}

const maxWindowItems = 8 // pre-allocate up to 8 window slots per provider

func createProviderMenuItems(p provider.Provider) *providerMenuItems {
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
		provider:      p,
		statusItem:    statusItem,
		windowItems:   windowItems,
		dashboardItem: dashboardItem,
	}
}

func updateUI(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus, menus map[string]*providerMenuItems, mReauth *systray.MenuItem) {
	hasExpiredClaude := false
	highestUsage := 0.0

	for name, data := range results {
		menu, ok := menus[name]
		if !ok {
			continue
		}

		if data == nil {
			menu.statusItem.SetTitle("No data")
			menu.statusItem.Show()
			continue
		}

		if data.IsExpired {
			if name == "claude" {
				hasExpiredClaude = true
			}
			expiredMsg := "Token expired"
			if data.Error != "" {
				expiredMsg = format.HumanizeError(data.Error)
			}
			menu.statusItem.SetTitle(expiredMsg)
			menu.statusItem.Show()
			continue
		}

		if data.Error != "" {
			menu.statusItem.SetTitle(fmt.Sprintf("Error: %s", format.HumanizeError(data.Error)))
			menu.statusItem.Show()
			continue
		}

		// Hide status, show window items
		menu.statusItem.Hide()

		// Check if this provider is "boring" (all windows at 0%)
		allZero := true
		for _, window := range data.Windows {
			if window.Utilization > 0 {
				allZero = false
				break
			}
		}

		if allZero && len(data.Windows) > 0 {
			// Show single collapsed line, hide the rest
			menu.windowItems[0].SetTitle("all clear")
			menu.windowItems[0].Show()
			for i := 1; i < len(menu.windowItems); i++ {
				menu.windowItems[i].Hide()
			}
			// Hide dashboard link for collapsed healthy providers
			menu.dashboardItem.Hide()
		} else {
			// Show full window breakdown
			for i, window := range data.Windows {
				if i >= len(menu.windowItems) {
					break // more windows than pre-allocated slots
				}

				proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
				resetStr := format.FormatDuration(time.Until(window.ResetsAt))
				menu.windowItems[i].SetTitle(fmt.Sprintf("%s: %.0f%% — %s — %s",
					window.Name, window.Utilization, resetStr, proj.PaceIndicator()))
				menu.windowItems[i].Show()

				if window.Utilization > highestUsage {
					highestUsage = window.Utilization
				}
			}

			// Hide unused window items
			for i := len(data.Windows); i < len(menu.windowItems); i++ {
				menu.windowItems[i].Hide()
			}

			// Ensure dashboard is visible for active providers
			menu.dashboardItem.Show()
		}
	}

	// Show/hide reauth button (only for Claude expired tokens)
	if hasExpiredClaude {
		mReauth.Show()
	} else {
		mReauth.Hide()
	}

	// Build display names map from provider menus
	displayNames := make(map[string]string, len(menus))
	for name, menu := range menus {
		displayNames[name] = menu.provider.DisplayName()
	}

	// Update icon and title based on worst usage
	updateTrayIcon(results)
	updateTrayTitle(results)
	updateTrayTooltip(results, statuses, displayNames)

	// Check notification thresholds
	checkThresholds(results, displayNames)
}

func updateTrayIcon(results map[string]*provider.UsageData) {
	worstProjected := 0.0

	for _, data := range results {
		if data == nil || data.Error != "" || data.IsExpired {
			continue
		}

		for _, window := range data.Windows {
			proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
			if proj.ProjectedPct > worstProjected {
				worstProjected = proj.ProjectedPct
			}
		}
	}

	switch {
	case worstProjected >= 100:
		setIconByName("red", icons.Red)
	case worstProjected >= 90:
		setIconByName("yellow", icons.Yellow)
	case worstProjected > 0:
		setIconByName("green", icons.Green)
	default:
		setIconByName("gray", icons.Gray)
	}
}

func updateTrayTitle(results map[string]*provider.UsageData) {
	var title string
	worstPct := 0.0
	worstProvider := ""

	for _, name := range sortedKeys(results) {
		data := results[name]
		if data == nil {
			continue
		}

		display := name
		// Capitalize first letter for display
		if len(display) > 0 {
			display = strings.ToUpper(display[:1]) + display[1:]
		}

		if data.IsExpired {
			// Expired always takes priority as the "worst"
			title = display + " expired"
			break
		}

		if data.Error != "" && len(data.Windows) == 0 {
			// Track error but keep looking for expired (higher priority)
			if title == "" {
				title = display + " error"
			}
			continue
		}

		for _, window := range data.Windows {
			proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
			if proj.ProjectedPct > worstPct {
				worstPct = proj.ProjectedPct
				worstProvider = display
			}
		}
	}

	// If no expired/error took priority, show the worst provider by projected usage
	if title == "" {
		if worstProvider != "" {
			title = fmt.Sprintf("%s %.0f%%", worstProvider, worstPct)
		} else {
			title = "clawmeter"
		}
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
		tooltip = "Clawmeter — no providers configured"
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
	if autostart.IsInstalled() {
		m.SetTitle("✓ Launch at login")
	} else {
		m.SetTitle("  Launch at login")
	}
}

func toggleAutostart(m *systray.MenuItem) {
	if autostart.IsInstalled() {
		autostart.Uninstall()
	} else {
		autostart.Install()
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

func sortedKeys(m map[string]*provider.UsageData) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

