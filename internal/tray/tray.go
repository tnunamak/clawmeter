//go:build tray

package tray

import (
	"context"
	"fmt"
	"math"
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
	systray.SetTooltip("Clawmeter — loading…")

	// ── Menu structure ──
	// Version header
	mHeader := systray.AddMenuItem(fmt.Sprintf("Clawmeter %s", version), "")
	mHeader.Disable()
	systray.AddSeparator()

	// Alert line — shows most urgent issue, hidden when all clear
	mAlert := systray.AddMenuItem("", "")
	mAlert.Disable()
	mAlert.Hide()

	// Create registry and register providers
	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	// Provider menu items
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

	// Refresh
	mRefresh := systray.AddMenuItem("Refresh now", "")

	systray.AddSeparator()

	// Reauth (hidden unless Claude is expired)
	mReauth := systray.AddMenuItem("Run `claude` to reauth", "")
	mReauth.Hide()

	// Update
	var pendingRelease *update.Release
	mUpdate := systray.AddMenuItem("", "")
	mUpdate.Hide()

	systray.AddSeparator()
	mAutostart := systray.AddMenuItem("", "")
	updateAutostartLabel(mAutostart)
	mQuit := systray.AddMenuItem("Quit", "")

	// ── Refresh machinery ──
	var refreshing sync.Mutex

	setRefreshLabel := func(title string, enabled bool) {
		mRefresh.SetTitle(title)
		if enabled {
			mRefresh.Enable()
		} else {
			mRefresh.Disable()
		}
	}

	var configuredNames []string
	for _, p := range registry.GetConfigured() {
		configuredNames = append(configuredNames, p.Name())
	}

	refresh := func() {
		if !refreshing.TryLock() {
			return
		}
		defer refreshing.Unlock()
		setRefreshLabel("Refreshing…", false)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Split providers: active fetch vs backed-off cache
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

		for name, cached := range skipped {
			result.Results[name] = cached
		}

		// Apply failure gate
		s.mu.Lock()
		for name, data := range result.Results {
			if _, wasSkipped := skipped[name]; wasSkipped {
				continue
			}
			if data == nil {
				continue
			}
			if data.Error == "" && !data.IsExpired {
				s.failureGate.RecordSuccess(name)
				continue
			}
			hasPrior := false
			if prev, ok := s.lastResults[name]; ok && prev != nil && prev.IsHealthy() {
				hasPrior = true
			}
			if !s.failureGate.ShouldSurfaceError(name, hasPrior) {
				if prev, ok := s.lastResults[name]; ok && prev != nil {
					result.Results[name] = prev
				}
			} else if hasPrior && len(data.Windows) == 0 {
				prev := s.lastResults[name]
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
		statuses := s.statuses
		s.mu.Unlock()

		updateUI(result.Results, statuses, providerMenus, mReauth, mAlert)
		setRefreshLabel("Refresh now · just now", true)
	}

	refreshStatus := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		statuses := status.FetchAll(ctx, configuredNames)
		s.mu.Lock()
		s.statuses = statuses
		lastResults := s.lastResults
		s.mu.Unlock()
		if lastResults != nil {
			updateUI(lastResults, statuses, providerMenus, mReauth, mAlert)
		}
	}

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

	applyUpdate := func() {
		if pendingRelease == nil {
			return
		}
		mUpdate.SetTitle("Updating…")
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

	// Show cached data immediately
	if cached, err := cache.Read(); err == nil && cached != nil {
		s.mu.Lock()
		s.lastResults = cached.ProviderData
		s.lastRefreshAt = cached.FetchedAt
		s.mu.Unlock()
		updateUI(cached.ProviderData, nil, providerMenus, mReauth, mAlert)
	}

	go refresh()
	go refreshStatus()
	go checkUpdate()

	// Tickers
	pollInterval := time.Duration(cfg.Settings.PollInterval) * time.Second
	if pollInterval < 60*time.Second {
		pollInterval = 5 * time.Minute
	}
	ticker := time.NewTicker(pollInterval)
	statusTicker := time.NewTicker(15 * time.Minute)
	updateTicker := time.NewTicker(updateCheckInterval)
	stalenessTicker := time.NewTicker(1 * time.Minute)

	go func() {
		for {
			select {
			case <-ticker.C:
				go refresh()
			case <-statusTicker.C:
				go refreshStatus()
			case <-updateTicker.C:
				go checkUpdate()
			case <-stalenessTicker.C:
				s.mu.Lock()
				t := s.lastRefreshAt
				s.mu.Unlock()
				if !t.IsZero() {
					setRefreshLabel(fmt.Sprintf("Refresh now · %s ago", format.FormatDuration(time.Since(t))), true)
				}
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

// ═══════════════════════════════════════════════════════════════
// Provider menu items
// ═══════════════════════════════════════════════════════════════

type providerMenuItems struct {
	provider    provider.Provider
	summaryItem *systray.MenuItem   // clickable, one-line: "● Claude — 66% / 33% left"
	windowItems []*systray.MenuItem // detail rows, shown on expand
	paceItem    *systray.MenuItem   // "⚠ 5h runs out in 38m at current pace"
	dashItem    *systray.MenuItem   // "Open Claude Dashboard"
	expanded    bool
	everHealthy bool

	// Track which detail items have content (since MenuItem.Title() is unexported)
	activeWindows int  // how many windowItems have been populated
	hasPaceWarn   bool // whether paceItem has content
}

const maxWindowItems = 8

func createProviderMenuItems(p provider.Provider) *providerMenuItems {
	summary := systray.AddMenuItem(p.DisplayName()+" — loading…", "")
	summary.Disable()

	windowItems := make([]*systray.MenuItem, maxWindowItems)
	for i := range windowItems {
		item := systray.AddMenuItem("", "")
		item.Disable()
		item.Hide()
		windowItems[i] = item
	}

	pace := systray.AddMenuItem("", "")
	pace.Disable()
	pace.Hide()

	dash := systray.AddMenuItem(fmt.Sprintf("  Open %s Dashboard", p.DisplayName()), p.DashboardURL())
	dash.Hide()
	go func() {
		for range dash.ClickedCh {
			openURL(p.DashboardURL())
		}
	}()

	systray.AddSeparator()

	pmi := &providerMenuItems{
		provider:    p,
		summaryItem: summary,
		windowItems: windowItems,
		paceItem:    pace,
		dashItem:    dash,
	}

	go func() {
		for range summary.ClickedCh {
			pmi.toggleExpand()
		}
	}()

	return pmi
}

func (pmi *providerMenuItems) toggleExpand() {
	pmi.expanded = !pmi.expanded
	if pmi.expanded {
		for i := 0; i < pmi.activeWindows && i < len(pmi.windowItems); i++ {
			pmi.windowItems[i].Show()
		}
		if pmi.hasPaceWarn {
			pmi.paceItem.Show()
		}
		pmi.dashItem.Show()
	} else {
		pmi.collapseDetail()
	}
}

func (pmi *providerMenuItems) collapseDetail() {
	pmi.expanded = false
	for _, w := range pmi.windowItems {
		w.Hide()
	}
	pmi.paceItem.Hide()
	pmi.dashItem.Hide()
}

func (pmi *providerMenuItems) hide() {
	pmi.summaryItem.Hide()
	pmi.collapseDetail()
}

// ═══════════════════════════════════════════════════════════════
// Urgency classification
// ═══════════════════════════════════════════════════════════════

type providerUrgency struct {
	tier            int // 0=expired, 1=errored, 2=critical(≥100%), 3=warning(≥90%), 4=healthy
	maxProjectedPct float64
	delta           float64
	worstWindow     string
	runsOutIn       time.Duration
}

func classify(data *provider.UsageData) providerUrgency {
	if data == nil {
		return providerUrgency{tier: 1}
	}
	if data.IsExpired {
		return providerUrgency{tier: 0, maxProjectedPct: 100}
	}
	if data.Error != "" && len(data.Windows) == 0 {
		return providerUrgency{tier: 1}
	}

	var maxPct, worstDelta float64
	var worstWin string
	var runsOut time.Duration
	for _, w := range data.Windows {
		proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))
		if proj.ProjectedPct > maxPct {
			maxPct = proj.ProjectedPct
			worstDelta = proj.Delta
			worstWin = w.Name
			runsOut = proj.RunsOutIn
		}
	}

	tier := 4
	if maxPct >= 100 {
		tier = 2
	} else if maxPct >= 90 {
		tier = 3
	}

	return providerUrgency{tier: tier, maxProjectedPct: maxPct, delta: worstDelta, worstWindow: worstWin, runsOutIn: runsOut}
}

// ═══════════════════════════════════════════════════════════════
// updateUI — the core refresh that rewrites all menu content
// ═══════════════════════════════════════════════════════════════

func updateUI(results map[string]*provider.UsageData, statuses map[string]*status.ProviderStatus, menus map[string]*providerMenuItems, mReauth, mAlert *systray.MenuItem) {
	hasExpiredClaude := false

	// ── Classify and sort ──
	type entry struct {
		name    string
		display string
		data    *provider.UsageData
		menu    *providerMenuItems
		u       providerUrgency
	}
	var entries []entry

	for name, menu := range menus {
		data := results[name]
		if data != nil && data.IsHealthy() {
			menu.everHealthy = true
		}
		// Hide never-healthy expired providers
		if data != nil && data.IsExpired && !menu.everHealthy {
			menu.hide()
			continue
		}
		if data != nil && data.IsExpired && name == "claude" {
			hasExpiredClaude = true
		}
		entries = append(entries, entry{
			name:    name,
			display: menu.provider.DisplayName(),
			data:    data,
			menu:    menu,
			u:       classify(data),
		})
	}

	// Sort: worst first
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].u.tier != entries[j].u.tier {
			return entries[i].u.tier < entries[j].u.tier
		}
		return entries[i].u.maxProjectedPct > entries[j].u.maxProjectedPct
	})

	// ── Alert header ──
	if len(entries) > 0 {
		worst := entries[0]
		switch {
		case worst.u.tier == 0:
			mAlert.SetTitle("✗ " + worst.display + " token expired")
			mAlert.Show()
		case worst.u.tier == 1:
			mAlert.SetTitle("✗ " + worst.display + " error")
			mAlert.Show()
		case worst.u.tier <= 3:
			label := worst.display
			if worst.u.worstWindow != "" {
				label += " " + worst.u.worstWindow
			}
			if worst.u.runsOutIn > 0 {
				mAlert.SetTitle(fmt.Sprintf("⚠ %s runs out in %s", label, format.FormatDuration(worst.u.runsOutIn)))
			} else {
				absDelta := math.Abs(worst.u.delta)
				pace := "on pace"
				if absDelta > 2 {
					if worst.u.delta > 0 {
						pace = fmt.Sprintf("%.0f%% behind", absDelta)
					} else {
						pace = fmt.Sprintf("%.0f%% ahead", absDelta)
					}
				}
				mAlert.SetTitle(fmt.Sprintf("⚠ %s %s", label, pace))
			}
			mAlert.Show()
		default:
			mAlert.Hide()
		}
	} else {
		mAlert.Hide()
	}

	// ── Update each provider row ──
	for _, e := range entries {
		m := e.menu
		data := e.data

		// No data
		if data == nil {
			m.summaryItem.SetTitle("? " + e.display + " — no data")
			m.summaryItem.Show()
			m.summaryItem.Disable()
			m.collapseDetail()
			continue
		}

		// Expired
		if data.IsExpired {
			msg := "expired, reauth needed"
			if data.Error != "" {
				msg = format.HumanizeError(data.Error)
			}
			m.summaryItem.SetTitle("✗ " + e.display + " — " + msg)
			m.summaryItem.Show()
			m.summaryItem.Disable()
			m.collapseDetail()
			continue
		}

		// Error with no windows
		if data.Error != "" && len(data.Windows) == 0 {
			m.summaryItem.SetTitle("? " + e.display + " — " + format.HumanizeError(data.Error))
			m.summaryItem.Show()
			m.summaryItem.Disable()
			m.collapseDetail()
			continue
		}

		// ── Healthy / has windows ──
		m.summaryItem.SetTitle(buildSummary(e.display, data, e.u))
		m.summaryItem.Show()
		m.summaryItem.Enable()

		// Populate detail rows
		var worstWinForPace *provider.UsageWindow
		var worstProj forecast.Projection

		activeCount := 0
		for i, w := range data.Windows {
			if i >= len(m.windowItems) {
				break
			}
			proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))

			left := 100 - w.Utilization
			if left < 0 {
				left = 0
			}

			resetStr := "just reset"
			if rem := time.Until(w.ResetsAt); rem > 0 {
				resetStr = "resets " + format.FormatDuration(rem)
			}

			verdict := "will last"
			if !proj.WillLastToReset && proj.RunsOutIn > 0 {
				verdict = "runs out " + format.FormatDuration(proj.RunsOutIn)
			}

			m.windowItems[i].SetTitle(fmt.Sprintf("  %-7s %2.0f%% left · %s · %s", w.Name, left, resetStr, verdict))
			activeCount++

			if !proj.WillLastToReset && proj.RunsOutIn > 0 {
				if worstWinForPace == nil || proj.ProjectedPct > worstProj.ProjectedPct {
					wCopy := w
					worstWinForPace = &wCopy
					worstProj = proj
				}
			}
		}
		for i := len(data.Windows); i < len(m.windowItems); i++ {
			m.windowItems[i].SetTitle("")
			m.windowItems[i].Hide()
		}
		m.activeWindows = activeCount

		if worstWinForPace != nil {
			m.paceItem.SetTitle(fmt.Sprintf("  ⚠ %s runs out in %s at current pace", worstWinForPace.Name, format.FormatDuration(worstProj.RunsOutIn)))
			m.hasPaceWarn = true
		} else {
			m.paceItem.SetTitle("")
			m.paceItem.Hide()
			m.hasPaceWarn = false
		}

		// If expanded, ensure new items are visible
		if m.expanded {
			for i := 0; i < m.activeWindows && i < len(m.windowItems); i++ {
				m.windowItems[i].Show()
			}
			if m.hasPaceWarn {
				m.paceItem.Show()
			}
			m.dashItem.Show()
		}
	}

	// Reauth button
	if hasExpiredClaude {
		mReauth.Show()
	} else {
		mReauth.Hide()
	}

	// ── Dynamic gauge icon ──
	displayNames := make(map[string]string, len(menus))
	activeResults := make(map[string]*provider.UsageData, len(results))
	for name, menu := range menus {
		displayNames[name] = menu.provider.DisplayName()
		if menu.everHealthy {
			activeResults[name] = results[name]
		}
	}

	bars := classifyForIcon(activeResults)
	if len(bars) > 0 {
		iconData := renderGaugeIcon(bars, 64)
		setDynamicIcon(iconData)
	} else {
		setIconByName("gray", icons.Gray)
	}

	updateTrayTitle(activeResults, displayNames)
	updateTrayTooltip(activeResults, statuses, displayNames)
	checkThresholds(results, displayNames)
}

// ═══════════════════════════════════════════════════════════════
// Summary line builder
// ═══════════════════════════════════════════════════════════════

func buildSummary(display string, data *provider.UsageData, u providerUrgency) string {
	dot := "●"
	if u.tier == 2 {
		dot = "✗"
	} else if u.tier == 3 {
		dot = "⚠"
	}

	if len(data.Windows) == 0 {
		return dot + " " + display
	}

	// Single window
	if len(data.Windows) == 1 {
		w := data.Windows[0]
		left := 100 - w.Utilization
		if left < 0 {
			left = 0
		}
		proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))
		if !proj.WillLastToReset && proj.RunsOutIn > 0 {
			return fmt.Sprintf("%s %s — %.0f%% left, runs out %s", dot, display, left, format.FormatDuration(proj.RunsOutIn))
		}
		return fmt.Sprintf("%s %s — %.0f%% left on %s", dot, display, left, w.Name)
	}

	// Multiple windows — if urgent, lead with the trouble window
	if u.runsOutIn > 0 && u.worstWindow != "" {
		for _, w := range data.Windows {
			if w.Name == u.worstWindow {
				left := 100 - w.Utilization
				if left < 0 {
					left = 0
				}
				return fmt.Sprintf("%s %s — %s at %.0f%%, runs out %s",
					dot, display, w.Name, left, format.FormatDuration(u.runsOutIn))
			}
		}
	}

	// Otherwise show compact % left per window
	parts := make([]string, 0, len(data.Windows))
	for _, w := range data.Windows {
		left := 100 - w.Utilization
		if left < 0 {
			left = 0
		}
		parts = append(parts, fmt.Sprintf("%.0f%%", left))
	}
	return fmt.Sprintf("%s %s — %s left", dot, display, strings.Join(parts, " / "))
}

// ═══════════════════════════════════════════════════════════════
// Tray title and tooltip
// ═══════════════════════════════════════════════════════════════

func updateTrayTitle(results map[string]*provider.UsageData, displayNames map[string]string) {
	type ranked struct {
		display string
		u       providerUrgency
		data    *provider.UsageData
	}
	var items []ranked
	for name, data := range results {
		d := displayNames[name]
		if d == "" {
			d = name
		}
		items = append(items, ranked{d, classify(data), data})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].u.tier != items[j].u.tier {
			return items[i].u.tier < items[j].u.tier
		}
		return items[i].u.maxProjectedPct > items[j].u.maxProjectedPct
	})

	var title string
	if len(items) == 0 {
		title = "clawmeter"
	} else {
		w := items[0]
		switch w.u.tier {
		case 0:
			title = w.display + " expired"
		case 1:
			title = w.display + " error"
		case 2, 3:
			if w.u.worstWindow != "" {
				title = fmt.Sprintf("%s %s tight", w.display, w.u.worstWindow)
			} else {
				title = w.display + " tight"
			}
		default:
			// All healthy — show least remaining
			worstLeft := 100.0
			worstD := ""
			for _, it := range items {
				if it.data == nil {
					continue
				}
				for _, win := range it.data.Windows {
					if l := 100 - win.Utilization; l < worstLeft {
						worstLeft = l
						worstD = it.display
					}
				}
			}
			if worstD != "" {
				title = fmt.Sprintf("%s %.0f%% left", worstD, worstLeft)
			} else {
				title = "clawmeter"
			}
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

		var lines []string
		for _, w := range data.Windows {
			left := 100 - w.Utilization
			if left < 0 {
				left = 0
			}
			rem := time.Until(w.ResetsAt)
			proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))
			verdict := "will last"
			if !proj.WillLastToReset && proj.RunsOutIn > 0 {
				verdict = "runs out " + format.FormatDuration(proj.RunsOutIn)
			}
			if rem <= 0 {
				lines = append(lines, fmt.Sprintf("  %s: %.0f%% left · just reset", w.Name, left))
			} else {
				lines = append(lines, fmt.Sprintf("  %s: %.0f%% left · resets %s · %s", w.Name, left, format.FormatDuration(rem), verdict))
			}
		}
		blocks = append(blocks, display+"\n"+strings.Join(lines, "\n"))
	}

	var tooltip string
	if len(blocks) > 0 {
		tooltip = strings.Join(blocks, "\n\n")
		s.mu.Lock()
		t := s.lastRefreshAt
		s.mu.Unlock()
		if !t.IsZero() {
			tooltip += "\n\nUpdated " + format.FormatDuration(time.Since(t)) + " ago"
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

// ═══════════════════════════════════════════════════════════════
// Notifications
// ═══════════════════════════════════════════════════════════════

func checkThresholds(results map[string]*provider.UsageData, displayNames map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastResults == nil {
		s.lastResults = make(map[string]*provider.UsageData)
	}

	warnAt := 80.0
	critAt := 95.0
	if cfg != nil {
		warnAt = cfg.Settings.NotificationThresholds.Warning
		critAt = cfg.Settings.NotificationThresholds.Critical
	}

	for name, data := range results {
		if data == nil || data.Error != "" {
			continue
		}
		oldData, hadOld := s.lastResults[name]
		for _, w := range data.Windows {
			pct := w.Utilization
			oldPct := 0.0
			if hadOld && oldData != nil {
				if ow, ok := oldData.GetWindow(w.Name); ok {
					oldPct = ow.Utilization
				}
			}
			display := displayNames[name]
			if display == "" {
				display = name
			}
			if pct >= critAt && oldPct < critAt {
				notify(fmt.Sprintf("%s usage critical", display),
					fmt.Sprintf("%s window at %.0f%% — rate limiting likely before reset", w.Name, pct), "critical")
			} else if pct >= warnAt && oldPct < warnAt {
				notify(fmt.Sprintf("%s usage warning", display),
					fmt.Sprintf("%s window at %.0f%% — on pace to reach limit", w.Name, pct), "normal")
			}
		}
	}
	s.lastResults = results
}

// ═══════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════

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
