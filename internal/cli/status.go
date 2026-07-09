package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	"github.com/tnunamak/clawmeter/internal/status"
)

const barWidth = 20

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func color(projectedPct float64) string {
	switch {
	case projectedPct >= 100:
		return "\033[31m" // red — projected to hit limit
	case projectedPct >= 90:
		return "\033[33m" // yellow — tight
	default:
		return "\033[32m" // green — on track
	}
}

const reset = "\033[0m"

func bar(pct float64) string {
	filled := int(math.Round(pct / 100 * barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}

// ProviderFormatter handles formatting for a single provider's output.
type ProviderFormatter struct {
	Name              string
	Display           string
	Data              *provider.UsageData
	Status            *status.ProviderStatus
	ExplicitlyEnabled bool
}

// FormatColor returns colorized multi-line output for a provider (legacy, unaligned).
func (pf *ProviderFormatter) FormatColor() []string {
	return pf.FormatColorAligned(len(pf.Display), 2)
}

// FormatColorAligned returns colorized output with consistent column widths.
func (pf *ProviderFormatter) FormatColorAligned(providerWidth, windowWidth int) []string {
	var statusLine string
	if pf.Status != nil && pf.Status.Indicator.HasIssue() {
		statusLine = pf.Status.FormatCLI()
	}
	if pf.Data != nil && pf.Data.Stale {
		statusLine = staleSummary(pf.Data)
	}

	pad := fmt.Sprintf("%%-%ds", providerWidth)

	winPad := fmt.Sprintf("%%-%ds", windowWidth)
	blankWin := fmt.Sprintf(winPad, "")

	if pf.Data == nil {
		line := fmt.Sprintf(pad+" %s %s%s%s (no data)", pf.Display, blankWin, color(0), bar(0), reset)
		if statusLine != "" {
			line += "  " + statusLine
		}
		return []string{line}
	}

	if pf.Data.IsExpired {
		expiredMsg := "expired"
		if pf.Data.Error != "" {
			expiredMsg += " — " + format.HumanizeError(pf.Data.Error)
		}
		line := fmt.Sprintf(pad+" %s %s%s%s  %s", pf.Display, blankWin, color(100), bar(100), reset, expiredMsg)
		if statusLine != "" {
			line += "  " + statusLine
		}
		return []string{line}
	}

	if pf.Data.Error != "" {
		line := fmt.Sprintf(pad+" %s %s%s%s  %s", pf.Display, blankWin, color(100), bar(0), reset, format.HumanizeError(pf.Data.Error))
		if statusLine != "" {
			line += "  " + statusLine
		}
		return []string{line}
	}

	windows := pf.Data.UsableWindows()
	lines := make([]string, 0, len(windows)+2)
	for i, window := range windows {
		proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
		resetStr := format.FormatDuration(time.Until(window.ResetsAt))

		label := pf.Display
		if i > 0 {
			label = ""
		}
		line := fmt.Sprintf(pad+" "+winPad+" %s%s%s %3.0f%%  resets %-7s %s",
			label, window.Name, color(proj.ProjectedPct), bar(window.Utilization), reset,
			window.Utilization, resetStr, proj.ColorIndicator())
		if i == 0 && statusLine != "" {
			line += "  " + statusLine
		}
		lines = append(lines, line)
	}
	if summary := resetCreditCompactSummary(pf.Data, time.Now()); summary != "" {
		label := ""
		if len(lines) == 0 {
			label = pf.Display
		}
		lines = append(lines, fmt.Sprintf(pad+" "+winPad+" %s", label, "resets", summary))
	}

	return lines
}

// FormatPlain returns plain text output for a provider.
func (pf *ProviderFormatter) FormatPlain() string {
	var suffix string
	if pf.Status != nil && pf.Status.Indicator.HasIssue() {
		suffix = " [" + pf.Status.Indicator.Label() + "]"
	}

	if pf.Data == nil {
		return fmt.Sprintf("%s: no data%s", pf.Display, suffix)
	}

	if pf.Data.IsExpired {
		expiredMsg := "expired"
		if pf.Data.Error != "" {
			expiredMsg += " — " + format.HumanizeError(pf.Data.Error)
		}
		return fmt.Sprintf("%s: %s%s", pf.Display, expiredMsg, suffix)
	}

	if pf.Data.Error != "" {
		return fmt.Sprintf("%s: error - %s%s", pf.Display, format.HumanizeError(pf.Data.Error), suffix)
	}

	windows := pf.Data.UsableWindows()
	parts := make([]string, 0, len(windows))
	for _, window := range windows {
		proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
		resetStr := format.FormatDuration(time.Until(window.ResetsAt))
		parts = append(parts, fmt.Sprintf("%s: %.0f%% (resets %s, %s)",
			window.Name, window.Utilization, resetStr, proj.PaceIndicator()))
	}

	prefix := ""
	if pf.Data.Stale {
		prefix = fmt.Sprintf("stale (updated %s) - ", pf.Data.FetchedAt.Format("15:04"))
	}
	if resetSummary := resetCreditPlainSummary(pf.Data, time.Now()); resetSummary != "" {
		parts = append(parts, resetSummary)
	}
	return fmt.Sprintf("%s: %s%s%s", pf.Display, prefix, strings.Join(parts, "  "), suffix)
}

func resetCreditPlainSummary(data *provider.UsageData, now time.Time) string {
	if data == nil || data.Stale || data.ResetCredits == nil {
		return ""
	}
	count := data.ResetCredits.DisplayCount(now)
	if count <= 0 {
		return ""
	}
	if expiresAt, ok := data.ResetCredits.EarliestExpiry(now); ok {
		return fmt.Sprintf("reset credits: %d available, earliest expires %s", count, formatResetCreditExpiry(expiresAt))
	}
	return fmt.Sprintf("reset credits: %d available", count)
}

func resetCreditCompactSummary(data *provider.UsageData, now time.Time) string {
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
		return fmt.Sprintf("%d %s - earliest expires %s", count, noun, formatResetCreditExpiry(expiresAt))
	}
	return fmt.Sprintf("%d %s available", count, noun)
}

func formatResetCreditExpiry(t time.Time) string {
	return t.Local().Format("Jan 2 3:04 PM")
}

// MultiProviderOutput handles displaying data from multiple providers.
type MultiProviderOutput struct {
	Providers []ProviderFormatter
}

// HideUnavailable removes auto-detected providers that have not produced
// useful usage data. Explicitly enabled providers remain visible so setup
// errors are actionable.
func (m *MultiProviderOutput) HideUnavailable() {
	filtered := make([]ProviderFormatter, 0, len(m.Providers))
	for _, pf := range m.Providers {
		if !provider.ShouldShowInPrimaryUI(pf.Data, false, pf.ExplicitlyEnabled) {
			continue
		}
		filtered = append(filtered, pf)
	}
	m.Providers = filtered
}

// PrintColor prints colorized output for all providers.
func (m *MultiProviderOutput) PrintColor() {
	// Compute column widths for alignment
	providerWidth := 0
	windowWidth := 0
	hasWindows := false
	for _, pf := range m.Providers {
		if len(pf.Display) > providerWidth {
			providerWidth = len(pf.Display)
		}
		if pf.Data != nil {
			for _, w := range pf.Data.UsableWindows() {
				hasWindows = true
				if len(w.Name) > windowWidth {
					windowWidth = len(w.Name)
				}
			}
		}
	}

	// Ensure column widths fit header labels
	if windowWidth < 6 {
		windowWidth = 6 // "WINDOW"
	}
	if providerWidth < 8 {
		providerWidth = 8 // "PROVIDER"
	}

	// Header row
	if hasWindows {
		dim := "\033[2m"
		// Columns: provider(pad) window(winPad) bar(20) " "pct(4)"  resets "time(7)" " pace
		hdr := fmt.Sprintf("%-*s %-*s %-*s %4s  resets %-7s %s",
			providerWidth, "PROVIDER",
			windowWidth, "WINDOW",
			barWidth, "USAGE",
			"PCT",
			"IN",
			"PACE")
		fmt.Printf("%s%s%s\n", dim, hdr, reset)
	}

	for i, pf := range m.Providers {
		lines := pf.FormatColorAligned(providerWidth, windowWidth)
		for _, line := range lines {
			fmt.Println(line)
		}
		if i < len(m.Providers)-1 && len(lines) > 0 {
			fmt.Println()
		}
	}
}

// PrintPlain prints plain text output for all providers, one per line.
func (m *MultiProviderOutput) PrintPlain() {
	for _, pf := range m.Providers {
		fmt.Println(pf.FormatPlain())
	}
}

// StatusLineSummary returns a compact, human-facing status segment for shell,
// tmux, terminal title, and harness statusline integrations.
func (m *MultiProviderOutput) StatusLineSummary() string {
	pf, window, proj, ok := m.worstReadableWindow()
	if !ok {
		return "CM no quota data"
	}

	prefix := "CM"
	if proj.ProjectedPct >= 100 {
		prefix = "CM!"
	} else if proj.ProjectedPct >= 90 {
		prefix = "CM~"
	}

	resetIn := time.Until(window.ResetsAt)
	line := fmt.Sprintf("%s %s %s est. %.0f%% reset %s",
		prefix, pf.Display, window.Name, proj.ProjectedPct, format.FormatDuration(resetIn))
	if !proj.WillLastToReset && proj.RunsOutIn > 0 {
		line += " out " + format.FormatDuration(proj.RunsOutIn)
	}
	return line
}

// AgentSummary returns a token-efficient but precise status for AI agents.
// It keeps exact seconds near boundaries so a tiny slice of a 7-day window is
// still visible to the agent.
func (m *MultiProviderOutput) AgentSummary() string {
	pf, window, proj, ok := m.worstReadableWindow()
	if !ok {
		return "Quota: no active quota data. Run `clawmeter providers` if setup may be incomplete."
	}

	resetIn := clampDuration(time.Until(window.ResetsAt))
	status := agentStatus(proj)

	parts := []string{
		fmt.Sprintf("Quota: worst=%s %s", pf.Display, window.Name),
		fmt.Sprintf("current=%s", formatPrecisePct(window.Utilization)),
		fmt.Sprintf("projected_at_reset=%s", formatPrecisePct(proj.ProjectedPct)),
		fmt.Sprintf("reset_in_seconds=%d", int64(resetIn.Seconds())),
		fmt.Sprintf("reset_in=%s", formatExactDuration(resetIn)),
		"status=" + status,
	}
	if !proj.WillLastToReset {
		if proj.RunsOutIn > 0 {
			runsOutIn := clampDuration(proj.RunsOutIn)
			parts = append(parts,
				fmt.Sprintf("runs_out_in_seconds=%d", int64(runsOutIn.Seconds())),
				"runs_out_in="+formatExactDuration(runsOutIn),
			)
		} else {
			parts = append(parts, "runs_out=now")
		}
		if proj.RunsOutEarlyBy > 0 {
			runsOutEarlyBy := clampDuration(proj.RunsOutEarlyBy)
			parts = append(parts,
				fmt.Sprintf("runs_out_early_by_seconds=%d", int64(runsOutEarlyBy.Seconds())),
				"runs_out_early_by="+formatExactDuration(runsOutEarlyBy),
			)
		}
	}
	if pf.Data != nil && pf.Data.Stale {
		parts = append(parts, "data=stale")
	}
	if quotas := m.agentQuotaSummaries(); len(quotas) > 0 {
		parts = append(parts, "quotas=["+strings.Join(quotas, " | ")+"]")
	}
	if resets := m.agentResetCreditSummaries(); len(resets) > 0 {
		parts = append(parts, "reset_credits=["+strings.Join(resets, " | ")+"]")
	}

	return strings.Join(parts, "; ") + "."
}

type agentQuotaSummary struct {
	Provider string
	Window   provider.UsageWindow
	Proj     forecast.Projection
	Status   string
	Tier     int
	Stale    bool
}

func (m *MultiProviderOutput) agentQuotaSummaries() []string {
	quotas := make([]agentQuotaSummary, 0)
	for i := range m.Providers {
		pf := &m.Providers[i]
		if pf.Data == nil || pf.Data.IsExpired || (pf.Data.Error != "" && !pf.Data.HasUsageWindows()) {
			continue
		}
		tier := classifyProvider(pf).tier
		for _, window := range pf.Data.UsableWindows() {
			proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
			quotas = append(quotas, agentQuotaSummary{
				Provider: pf.Display,
				Window:   window,
				Proj:     proj,
				Status:   agentStatus(proj),
				Tier:     tier,
				Stale:    pf.Data.Stale,
			})
		}
	}

	sort.SliceStable(quotas, func(i, j int) bool {
		a, b := quotas[i], quotas[j]
		if a.Tier != b.Tier {
			return a.Tier < b.Tier
		}
		if cmp := forecast.CompareRisk(a.Proj, b.Proj); cmp != 0 {
			return cmp < 0
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Window.Name < b.Window.Name
	})

	out := make([]string, 0, len(quotas))
	for _, quota := range quotas {
		resetIn := clampDuration(time.Until(quota.Window.ResetsAt))
		fields := []string{
			fmt.Sprintf("current=%s", formatPrecisePct(quota.Window.Utilization)),
			fmt.Sprintf("projected_at_reset=%s", formatPrecisePct(quota.Proj.ProjectedPct)),
			fmt.Sprintf("reset_in=%s", formatExactDuration(resetIn)),
			"status=" + quota.Status,
		}
		if !quota.Proj.WillLastToReset {
			if quota.Proj.RunsOutIn > 0 {
				runsOutIn := clampDuration(quota.Proj.RunsOutIn)
				fields = append(fields,
					fmt.Sprintf("runs_out_in_seconds=%d", int64(runsOutIn.Seconds())),
					"runs_out_in="+formatExactDuration(runsOutIn),
				)
			} else {
				fields = append(fields, "runs_out=now")
			}
			if quota.Proj.RunsOutEarlyBy > 0 {
				runsOutEarlyBy := clampDuration(quota.Proj.RunsOutEarlyBy)
				fields = append(fields,
					fmt.Sprintf("runs_out_early_by_seconds=%d", int64(runsOutEarlyBy.Seconds())),
					"runs_out_early_by="+formatExactDuration(runsOutEarlyBy),
				)
			}
		}
		if quota.Stale {
			fields = append(fields, "data=stale")
		}
		out = append(out, fmt.Sprintf("%s %s(%s)", quota.Provider, quota.Window.Name, strings.Join(fields, ",")))
	}
	return out
}

func (m *MultiProviderOutput) agentResetCreditSummaries() []string {
	type resetSummary struct {
		text      string
		expiresAt time.Time
		hasExpiry bool
		provider  string
		count     int
	}
	now := time.Now()
	summaries := make([]resetSummary, 0)
	for i := range m.Providers {
		pf := &m.Providers[i]
		if pf.Data == nil || pf.Data.Stale || pf.Data.ResetCredits == nil {
			continue
		}
		count := pf.Data.ResetCredits.DisplayCount(now)
		if count <= 0 {
			continue
		}
		fields := []string{
			pf.Display,
			fmt.Sprintf("available=%d", count),
		}
		expiresAt, hasExpiry := pf.Data.ResetCredits.EarliestExpiry(now)
		if hasExpiry {
			fields = append(fields,
				"earliest_expires_at="+expiresAt.Local().Format(time.RFC3339),
				"earliest_expires_in="+formatExactDuration(expiresAt.Sub(now)),
			)
		}
		summaries = append(summaries, resetSummary{
			text:      strings.Join(fields, " "),
			expiresAt: expiresAt,
			hasExpiry: hasExpiry,
			provider:  pf.Display,
			count:     count,
		})
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		a, b := summaries[i], summaries[j]
		if a.hasExpiry != b.hasExpiry {
			return a.hasExpiry
		}
		if a.hasExpiry && !a.expiresAt.Equal(b.expiresAt) {
			return a.expiresAt.Before(b.expiresAt)
		}
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		return a.count > b.count
	})
	out := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, summary.text)
	}
	return out
}

func agentStatus(proj forecast.Projection) string {
	if proj.ProjectedPct >= 100 {
		return "at_risk"
	}
	if proj.ProjectedPct >= 90 {
		return "tight"
	}
	return "on_track"
}

func (m *MultiProviderOutput) worstReadableWindow() (*ProviderFormatter, provider.UsageWindow, forecast.Projection, bool) {
	if len(m.Providers) == 0 {
		return nil, provider.UsageWindow{}, forecast.Projection{}, false
	}
	sortProvidersByUrgency(m.Providers)

	var bestPF *ProviderFormatter
	var bestWindow provider.UsageWindow
	var bestProj forecast.Projection
	var bestTier = 5

	for i := range m.Providers {
		pf := &m.Providers[i]
		if pf.Data == nil || pf.Data.IsExpired || (pf.Data.Error != "" && !pf.Data.HasUsageWindows()) {
			continue
		}
		tier := classifyProvider(pf).tier
		for _, window := range pf.Data.UsableWindows() {
			proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
			if bestPF == nil || tier < bestTier || (tier == bestTier && forecast.CompareRisk(proj, bestProj) < 0) {
				bestPF = pf
				bestWindow = window
				bestProj = proj
				bestTier = tier
			}
		}
	}

	if bestPF == nil {
		return nil, provider.UsageWindow{}, forecast.Projection{}, false
	}
	return bestPF, bestWindow, bestProj, true
}

func clampDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d.Round(time.Second)
}

func formatExactDuration(d time.Duration) string {
	d = clampDuration(d)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int64(d.Seconds()))
	}
	return format.FormatDuration(d)
}

func formatPrecisePct(pct float64) string {
	absPct := math.Abs(pct)
	nearBoundary := math.Abs(pct-90) <= 1 || math.Abs(pct-100) <= 1
	var s string
	switch {
	case absPct > 0 && absPct < 1:
		s = fmt.Sprintf("%.2f", pct)
	case nearBoundary:
		s = fmt.Sprintf("%.2f", pct)
	default:
		s = fmt.Sprintf("%.1f", pct)
	}
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s + "%"
}

func roundPct(pct float64) float64 {
	return math.Round(pct*100) / 100
}

// JSONOutput represents the JSON structure for multi-provider output.
type JSONOutput struct {
	Providers map[string]*ProviderJSONOutput `json:"providers"`
	FetchedAt time.Time                      `json:"fetched_at,omitempty"`
	Cache     *CacheInfo                     `json:"cache,omitempty"`
}

// ProviderJSONOutput is the JSON representation for a single provider.
type ProviderJSONOutput struct {
	Usage    *provider.UsageData    `json:"usage,omitempty"`
	Forecast *JSONForecast          `json:"forecast,omitempty"`
	Status   *status.ProviderStatus `json:"status,omitempty"`
}

// JSONForecast contains forecast data.
type JSONForecast struct {
	Windows map[string]JSONProjection `json:"windows"`
}

// JSONProjection is a single window projection.
type JSONProjection struct {
	ProjectedPct float64 `json:"projected_pct"`
	Indicator    string  `json:"indicator"`
}

// CacheInfo contains cache metadata.
type CacheInfo struct {
	Hit       bool      `json:"hit"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

// PrintJSON prints JSON output for all providers.
func (m *MultiProviderOutput) PrintJSON(cacheEntry *cache.Entry) {
	out := JSONOutput{
		Providers: make(map[string]*ProviderJSONOutput),
	}

	if cacheEntry != nil {
		out.FetchedAt = cacheEntry.FetchedAt
		out.Cache = &CacheInfo{Hit: true, FetchedAt: cacheEntry.FetchedAt}
	} else {
		out.FetchedAt = time.Now()
	}

	for _, pf := range m.Providers {
		if pf.Data == nil {
			continue
		}

		providerOut := &ProviderJSONOutput{
			Usage: pf.Data,
		}

		// Add forecasts for each window
		windows := pf.Data.UsableWindows()
		if len(windows) > 0 {
			providerOut.Forecast = &JSONForecast{
				Windows: make(map[string]JSONProjection),
			}
			for _, window := range windows {
				proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
				providerOut.Forecast.Windows[window.Name] = JSONProjection{
					ProjectedPct: roundPct(proj.ProjectedPct),
					Indicator:    proj.Indicator(),
				}
			}
		}

		if pf.Status != nil {
			providerOut.Status = pf.Status
		}

		out.Providers[pf.Name] = providerOut
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: json error: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

// Status fetches and displays usage status for all configured providers.
func Status(jsonMode, plainMode, showAll bool) int {
	output, cacheEntry, code := loadStatusOutput(showAll)
	if code != 0 {
		return code
	}
	printOutput(output, jsonMode, plainMode, cacheEntry)
	return 0
}

// StatusLine prints one compact line for statusline/prompt/tmux integrations.
func StatusLine(showAll bool) int {
	output, code := loadCachedStatusOutput(showAll)
	if code != 0 {
		return code
	}
	fmt.Println(output.StatusLineSummary())
	return 0
}

// StatusAgent prints one precise, token-efficient line for AI agents.
func StatusAgent(showAll bool) int {
	output, _, code := loadStatusOutput(showAll)
	if code != 0 {
		return code
	}
	fmt.Println(output.AgentSummary())
	return 0
}

func loadStatusOutput(showAll bool) (*MultiProviderOutput, *cache.Entry, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return nil, nil, 1
	}

	// Create registry and register providers
	registry := provider.NewRegistry()

	all.Register(registry, cfg)

	// Try cache first — but only if it covers everything currently
	// configured. Otherwise (e.g., a provider became configured via
	// `codex login` after the last fetch) we'd return stale-by-membership
	// data and the user would see nothing for ~60s.
	configuredNames := registry.ConfiguredNames()
	if cacheEntry, err := cache.Read(); err == nil && cacheEntry.IsValid() && cacheEntry.Covers(configuredNames) && !cacheEntry.HasStaleData(configuredNames) {
		output := buildOutputFromCache(registry, cfg, cacheEntry)
		if showAll {
			output.IncludeAllProviders(registry, cfg)
		} else {
			output.HideUnavailable()
		}
		return output, cacheEntry, 0
	}

	// Fetch fresh data from all configured providers
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch usage and status in parallel
	var result *provider.MultiFetchResult
	var statuses map[string]*status.ProviderStatus
	done := make(chan struct{}, 2)

	go func() {
		result = provider.FetchAllParallel(ctx, registry)

		// For providers that errored, fall back to cached data if available
		if cacheEntry, err := cache.Read(); err == nil && cacheEntry != nil {
			for name, data := range result.Results {
				if data != nil && data.Error != "" && !data.HasUsageWindows() {
					if cached, ok := staleFallback(cacheEntry, name, data.Error); ok {
						result.Results[name] = cached
					}
				}
			}
		}

		_ = cache.Write(result)
		done <- struct{}{}
	}()
	go func() {
		var names []string
		for _, p := range registry.GetConfigured() {
			names = append(names, p.Name())
		}
		statuses = status.FetchAll(ctx, names)
		done <- struct{}{}
	}()
	<-done
	<-done

	// Build output
	output := buildOutputFromResult(registry, cfg, result, statuses)
	if showAll {
		output.IncludeAllProviders(registry, cfg)
	} else {
		output.HideUnavailable()
	}

	return output, nil, 0
}

func loadCachedStatusOutput(showAll bool) (*MultiProviderOutput, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return nil, 1
	}

	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	cacheEntry, err := cache.Read()
	if err != nil || cacheEntry == nil {
		return &MultiProviderOutput{}, 0
	}

	output := buildOutputFromCache(registry, cfg, cacheEntry)
	if showAll {
		output.IncludeAllProviders(registry, cfg)
	} else {
		output.HideUnavailable()
	}
	return output, 0
}

// IncludeAllProviders appends registered providers that were not fetched
// because they are unavailable, disabled, or opt-in. This makes `status --all`
// a true inventory view without polling providers that lack usable auth.
func (m *MultiProviderOutput) IncludeAllProviders(registry *provider.Registry, cfg *config.Config) {
	seen := make(map[string]struct{}, len(m.Providers))
	for _, pf := range m.Providers {
		seen[pf.Name] = struct{}{}
	}
	for _, p := range registry.GetAll() {
		if _, ok := seen[p.Name()]; ok {
			continue
		}
		m.Providers = append(m.Providers, ProviderFormatter{
			Name:              p.Name(),
			Display:           p.DisplayName(),
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(p.Name()),
		})
	}
}

func staleFallback(cacheEntry *cache.Entry, name, reason string) (*provider.UsageData, bool) {
	cached, ok := cacheEntry.GetProvider(name)
	if !ok || cached == nil || !cached.HasUsageWindows() {
		return nil, false
	}
	cached = cached.Clone()
	cached.MarkStale(reason)
	return cached, true
}

func staleReason(data *provider.UsageData) string {
	if data == nil || data.Warning == "" {
		return "usage unavailable"
	}
	return format.HumanizeError(data.Warning)
}

func staleSummary(data *provider.UsageData) string {
	reason := staleReason(data)
	if reason == "" || reason == "usage unavailable" {
		return "stale: showing last good data"
	}
	return "stale: showing last good data (" + reason + ")"
}

// Check fetches usage and returns an exit code: 0=healthy, 1=warning, 2=critical/expired/error.
func Check() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 2
	}

	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	if len(registry.GetConfigured()) == 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: no providers configured\n")
		return 2
	}

	// Try cache first — but skip if a newly-configured provider has no
	// cached entry yet (same stale-by-membership concern as Status()).
	configuredNames := registry.ConfiguredNames()
	var output *MultiProviderOutput
	if cacheEntry, err := cache.Read(); err == nil && cacheEntry.IsValid() && cacheEntry.Covers(configuredNames) && !cacheEntry.HasStaleData(configuredNames) {
		output = buildOutputFromCache(registry, cfg, cacheEntry)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := provider.FetchAllParallel(ctx, registry)
		if cacheEntry, err := cache.Read(); err == nil && cacheEntry != nil {
			for name, data := range result.Results {
				if data != nil && data.Error != "" && !data.HasUsageWindows() {
					if cached, ok := staleFallback(cacheEntry, name, data.Error); ok {
						result.Results[name] = cached
					}
				}
			}
		}
		_ = cache.Write(result)
		output = buildOutputFromResult(registry, cfg, result, nil)
	}
	output.HideUnavailable()
	if len(output.Providers) == 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: no active providers\n")
		return 2
	}

	worstTier := 4
	for i := range output.Providers {
		u := classifyProvider(&output.Providers[i])
		if u.tier < worstTier {
			worstTier = u.tier
		}
	}

	// Map tiers to exit codes: 0,1,2 → exit 2; 3 → exit 1; 4 → exit 0
	switch {
	case worstTier <= 2:
		return 2
	case worstTier == 3:
		return 1
	default:
		return 0
	}
}

// buildOutputFromCache creates output from cached data.
func buildOutputFromCache(registry *provider.Registry, cfg *config.Config, cacheEntry *cache.Entry) *MultiProviderOutput {
	output := &MultiProviderOutput{
		Providers: make([]ProviderFormatter, 0),
	}

	// Only show configured providers
	for _, p := range registry.GetConfigured() {
		data, _ := cacheEntry.GetProvider(p.Name())
		output.Providers = append(output.Providers, ProviderFormatter{
			Name:              p.Name(),
			Display:           p.DisplayName(),
			Data:              data,
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(p.Name()),
		})
	}

	return output
}

// buildOutputFromResult creates output from fetch results.
func buildOutputFromResult(registry *provider.Registry, cfg *config.Config, result *provider.MultiFetchResult, statuses map[string]*status.ProviderStatus) *MultiProviderOutput {
	output := &MultiProviderOutput{
		Providers: make([]ProviderFormatter, 0),
	}

	// Only show configured providers that have data
	for _, p := range registry.GetConfigured() {
		data, ok := result.Results[p.Name()]
		if !ok {
			continue
		}
		output.Providers = append(output.Providers, ProviderFormatter{
			Name:              p.Name(),
			Display:           p.DisplayName(),
			Data:              data,
			Status:            statuses[p.Name()],
			ExplicitlyEnabled: cfg.IsProviderExplicitlyEnabled(p.Name()),
		})
	}

	return output
}

// providerUrgency classifies a provider for sorting and summary.
type providerUrgency struct {
	tier            int // 0=expired, 1=errored, 2=critical(>=100%), 3=warning(>=90%), 4=healthy
	maxProjectedPct float64
	worstWindow     string
	worstProjection forecast.Projection
	runsOutIn       time.Duration
	runsOutEarlyBy  time.Duration
}

func classifyProvider(pf *ProviderFormatter) providerUrgency {
	if pf.Data == nil {
		if pf.ExplicitlyEnabled {
			return providerUrgency{tier: 1} // enabled but missing data => actionable setup issue
		}
		return providerUrgency{tier: 5} // passive unavailable providers belong at the bottom of --all
	}
	if pf.Data.IsExpired {
		return providerUrgency{tier: 0, maxProjectedPct: 100}
	}
	if pf.Data.Error != "" && !pf.Data.HasUsageWindows() {
		return providerUrgency{tier: 1}
	}

	var maxPct float64
	var worstWindow string
	var worstProjection forecast.Projection
	var hasWorstProjection bool
	var runsOutIn time.Duration
	var runsOutEarlyBy time.Duration
	for _, w := range pf.Data.UsableWindows() {
		proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))
		if proj.ProjectedPct > maxPct {
			maxPct = proj.ProjectedPct
		}
		if !hasWorstProjection || forecast.CompareRisk(proj, worstProjection) < 0 {
			worstWindow = w.Name
			worstProjection = proj
			hasWorstProjection = true
			runsOutIn = proj.RunsOutIn
			runsOutEarlyBy = proj.RunsOutEarlyBy
		}
	}

	tier := 4
	switch {
	case maxPct >= 100:
		tier = 2
	case maxPct >= 90 || pf.Data.Stale:
		tier = 3
	}

	return providerUrgency{
		tier:            tier,
		maxProjectedPct: maxPct,
		worstWindow:     worstWindow,
		worstProjection: worstProjection,
		runsOutIn:       runsOutIn,
		runsOutEarlyBy:  runsOutEarlyBy,
	}
}

// sortProvidersByUrgency sorts providers so the most urgent appear first.
func sortProvidersByUrgency(providers []ProviderFormatter) {
	sort.SliceStable(providers, func(i, j int) bool {
		ui := classifyProvider(&providers[i])
		uj := classifyProvider(&providers[j])
		if ui.tier != uj.tier {
			return ui.tier < uj.tier
		}
		if cmp := forecast.CompareRisk(ui.worstProjection, uj.worstProjection); cmp != 0 {
			return cmp < 0
		}
		return ui.maxProjectedPct > uj.maxProjectedPct
	})
}

// printSummary prints a one-line summary before the per-provider details.
// Only shown when there are 2+ providers.
func printSummary(output *MultiProviderOutput, colorMode bool) {
	if len(output.Providers) < 2 {
		return
	}

	var healthy, warning, critical, errored, expired int
	var worstIdx int
	var worstU providerUrgency
	worstU.tier = 5 // sentinel

	for i := range output.Providers {
		u := classifyProvider(&output.Providers[i])
		switch u.tier {
		case 0:
			expired++
		case 1:
			errored++
		case 2:
			critical++
		case 3:
			warning++
		case 4:
			healthy++
		}
		if u.tier < worstU.tier || (u.tier == worstU.tier && forecast.CompareRisk(u.worstProjection, worstU.worstProjection) < 0) {
			worstIdx = i
			worstU = u
		}
	}

	if healthy+warning+critical+errored+expired == 0 {
		return
	}

	var line string

	switch {
	case expired == 0 && errored == 0 && critical == 0 && warning == 0:
		line = fmt.Sprintf("All clear — %d providers healthy", healthy)

	case worstU.tier == 0:
		rest := summaryCounts(healthy, warning, critical, errored, expired-1)
		line = fmt.Sprintf("✗ %s expired", output.Providers[worstIdx].Display)
		if rest != "" {
			line += " — " + rest
		}

	case worstU.tier == 1:
		rest := summaryCounts(healthy, warning, critical, errored-1, expired)
		line = fmt.Sprintf("✗ %s error", output.Providers[worstIdx].Display)
		if rest != "" {
			line += " — " + rest
		}

	default:
		// Warning or critical — show the worst window
		windowLabel := output.Providers[worstIdx].Display
		if worstU.worstWindow != "" {
			windowLabel += " " + worstU.worstWindow
		}
		etaStr := ""
		if worstU.runsOutIn > 0 {
			etaStr = fmt.Sprintf(" (runs out in %s)", format.FormatDuration(worstU.runsOutIn))
		} else if worstU.runsOutEarlyBy > 0 {
			etaStr = " (out now)"
		}

		// Subtract worst from its own category for the rest count
		var rest string
		switch worstU.tier {
		case 2:
			rest = summaryCounts(healthy, warning, critical-1, errored, expired)
		case 3:
			rest = summaryCounts(healthy, warning-1, critical, errored, expired)
		default:
			rest = summaryCounts(healthy-1, warning, critical, errored, expired)
		}

		paceWord := forecast.PaceLabel(worstU.worstProjection.ProjectedPct)
		line = fmt.Sprintf("⚠ %s %s%s", windowLabel, paceWord, etaStr)
		if rest != "" {
			line += " — " + rest
		}
	}

	if colorMode {
		var ansi string
		switch {
		case worstU.tier <= 1:
			ansi = "\033[31m"
		case worstU.tier <= 3:
			ansi = "\033[33m"
		default:
			ansi = "\033[32m"
		}
		fmt.Printf("%s%s%s\n\n", ansi, line, reset)
	} else {
		fmt.Println(line)
	}
}

// summaryCounts builds a compact "N healthy, N warning" string, omitting zero counts.
func summaryCounts(healthy, warning, critical, errored, expired int) string {
	var parts []string
	if healthy > 0 {
		parts = append(parts, fmt.Sprintf("%d healthy", healthy))
	}
	if warning > 0 {
		parts = append(parts, fmt.Sprintf("%d warning", warning))
	}
	if critical > 0 {
		parts = append(parts, fmt.Sprintf("%d critical", critical))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("%d errored", errored))
	}
	if expired > 0 {
		parts = append(parts, fmt.Sprintf("%d expired", expired))
	}
	return strings.Join(parts, ", ")
}

// printOutput prints the output in the requested format.
func printOutput(output *MultiProviderOutput, jsonMode, plainMode bool, cacheEntry *cache.Entry) {
	// Check if no providers are configured
	if len(output.Providers) == 0 {
		if jsonMode {
			fmt.Println(`{"error": "no active providers"}`)
		} else {
			fmt.Println("no active providers")
		}
		return
	}

	// Sort providers by urgency (most urgent first)
	sortProvidersByUrgency(output.Providers)

	if jsonMode {
		output.PrintJSON(cacheEntry)
	} else if plainMode || !isTTY() {
		output.PrintPlain()
	} else {
		output.PrintColor()
	}
}

// SingleProviderStatus fetches status for a single provider (backward compatibility).
func SingleProviderStatus(providerName string, jsonMode, plainMode bool) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	registry := provider.NewRegistry()
	all.Register(registry, cfg)

	p, ok := registry.Get(providerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "clawmeter: unknown provider %q\n", providerName)
		return 1
	}

	if cfg.IsProviderDisabled(providerName) {
		fmt.Fprintf(os.Stderr, "clawmeter: provider %q is disabled (run 'clawmeter config enable %s')\n", providerName, providerName)
		return 2
	}

	if !p.IsConfigured() {
		fmt.Fprintf(os.Stderr, "clawmeter: provider %q is not configured\n", providerName)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fetch usage and status in parallel
	var data *provider.UsageData
	var fetchErr error
	var ps *status.ProviderStatus
	done := make(chan struct{}, 2)

	go func() {
		data, fetchErr = p.FetchUsage(ctx)
		done <- struct{}{}
	}()
	go func() {
		ps = status.Fetch(ctx, providerName)
		done <- struct{}{}
	}()
	<-done
	<-done

	if fetchErr != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", fetchErr)
		return 1
	}
	if data != nil && data.Error != "" && !data.HasUsageWindows() {
		if cacheEntry, err := cache.Read(); err == nil && cacheEntry != nil {
			if cached, ok := staleFallback(cacheEntry, p.Name(), data.Error); ok {
				data = cached
			}
		}
	}

	pf := ProviderFormatter{
		Name:    p.Name(),
		Display: p.DisplayName(),
		Data:    data,
		Status:  ps,
	}

	output := &MultiProviderOutput{
		Providers: []ProviderFormatter{pf},
	}

	if jsonMode {
		output.PrintJSON(nil)
	} else if plainMode || !isTTY() {
		output.PrintPlain()
	} else {
		output.PrintColor()
	}

	return 0
}
