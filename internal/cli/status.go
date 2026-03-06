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
	Name    string
	Display string
	Data    *provider.UsageData
	Status  *status.ProviderStatus
}

// FormatColor returns colorized multi-line output for a provider.
func (pf *ProviderFormatter) FormatColor() []string {
	// Status issue line (shown before usage if there's a problem)
	var statusLine string
	if pf.Status != nil && pf.Status.Indicator.HasIssue() {
		statusLine = pf.Status.FormatCLI()
	}

	if pf.Data == nil {
		line := fmt.Sprintf("%s  %s%s%s (no data)", pf.Display, color(0), bar(0), reset)
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
		line := fmt.Sprintf("%s  %s%s%s  %s", pf.Display, color(100), bar(100), reset, expiredMsg)
		if statusLine != "" {
			line += "  " + statusLine
		}
		return []string{line}
	}

	if pf.Data.Error != "" {
		line := fmt.Sprintf("%s  %s%s%s  error: %s", pf.Display, color(100), bar(0), reset, format.HumanizeError(pf.Data.Error))
		if statusLine != "" {
			line += "  " + statusLine
		}
		return []string{line}
	}

	lines := make([]string, 0, len(pf.Data.Windows)+1)
	for i, window := range pf.Data.Windows {
		proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
		resetStr := format.FormatDuration(time.Until(window.ResetsAt))

		label := pf.Display
		if i > 0 {
			label = ""
		}
		line := fmt.Sprintf("%-9s %2s %s%s%s %3.0f%%  resets %s  %s",
			label, window.Name, color(proj.ProjectedPct), bar(window.Utilization), reset,
			window.Utilization, resetStr, proj.ColorIndicator())
		if i == 0 && statusLine != "" {
			line += "  " + statusLine
		}
		lines = append(lines, line)
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

	parts := make([]string, 0, len(pf.Data.Windows))
	for _, window := range pf.Data.Windows {
		proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
		resetStr := format.FormatDuration(time.Until(window.ResetsAt))
		parts = append(parts, fmt.Sprintf("%s: %.0f%% (resets %s, %s)",
			window.Name, window.Utilization, resetStr, proj.Indicator()))
	}

	return fmt.Sprintf("%s: %s%s", pf.Display, strings.Join(parts, "  "), suffix)
}

// MultiProviderOutput handles displaying data from multiple providers.
type MultiProviderOutput struct {
	Providers []ProviderFormatter
}

// PrintColor prints colorized output for all providers.
func (m *MultiProviderOutput) PrintColor() {
	for i, pf := range m.Providers {
		lines := pf.FormatColor()
		for _, line := range lines {
			fmt.Println(line)
		}
		// Add blank line between providers (but not after the last one)
		if i < len(m.Providers)-1 && len(lines) > 0 {
			fmt.Println()
		}
	}
}

// PrintPlain prints plain text output for all providers.
func (m *MultiProviderOutput) PrintPlain() {
	parts := make([]string, 0, len(m.Providers))
	for _, pf := range m.Providers {
		parts = append(parts, pf.FormatPlain())
	}
	fmt.Println(strings.Join(parts, "  "))
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
		if len(pf.Data.Windows) > 0 {
			providerOut.Forecast = &JSONForecast{
				Windows: make(map[string]JSONProjection),
			}
			for _, window := range pf.Data.Windows {
				proj := forecast.Project(window.Utilization, window.ResetsAt, forecast.GuessWindowType(window.Name))
				providerOut.Forecast.Windows[window.Name] = JSONProjection{
					ProjectedPct: math.Round(proj.ProjectedPct),
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
func Status(jsonMode, plainMode bool) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	// Create registry and register providers
	registry := provider.NewRegistry()

	all.Register(registry, cfg)

	// Try cache first
	if cacheEntry, err := cache.Read(); err == nil && cacheEntry.IsValid() {
		output := buildOutputFromCache(registry, cacheEntry)
		printOutput(output, jsonMode, plainMode, cacheEntry)
		return 0
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
				if data != nil && data.Error != "" && len(data.Windows) == 0 {
					if cached, ok := cacheEntry.GetProvider(name); ok && cached.IsHealthy() {
						cached.Error = data.Error + " (showing cached)"
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
	output := buildOutputFromResult(registry, result, statuses)
	printOutput(output, jsonMode, plainMode, nil)

	return 0
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

	// Try cache first
	var output *MultiProviderOutput
	if cacheEntry, err := cache.Read(); err == nil && cacheEntry.IsValid() {
		output = buildOutputFromCache(registry, cacheEntry)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := provider.FetchAllParallel(ctx, registry)
		_ = cache.Write(result)
		output = buildOutputFromResult(registry, result, nil)
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
func buildOutputFromCache(registry *provider.Registry, cacheEntry *cache.Entry) *MultiProviderOutput {
	output := &MultiProviderOutput{
		Providers: make([]ProviderFormatter, 0),
	}

	// Only show configured providers
	for _, p := range registry.GetConfigured() {
		data, _ := cacheEntry.GetProvider(p.Name())
		output.Providers = append(output.Providers, ProviderFormatter{
			Name:    p.Name(),
			Display: p.DisplayName(),
			Data:    data,
		})
	}

	return output
}

// buildOutputFromResult creates output from fetch results.
func buildOutputFromResult(registry *provider.Registry, result *provider.MultiFetchResult, statuses map[string]*status.ProviderStatus) *MultiProviderOutput {
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
			Name:    p.Name(),
			Display: p.DisplayName(),
			Data:    data,
			Status:  statuses[p.Name()],
		})
	}

	return output
}

// providerUrgency classifies a provider for sorting and summary.
type providerUrgency struct {
	tier            int // 0=expired, 1=errored, 2=critical(>=100%), 3=warning(>=90%), 4=healthy
	maxProjectedPct float64
	worstWindow     string
	runsOutIn       time.Duration
}

func classifyProvider(pf *ProviderFormatter) providerUrgency {
	if pf.Data == nil {
		return providerUrgency{tier: 1} // no data => treat as errored
	}
	if pf.Data.IsExpired {
		return providerUrgency{tier: 0, maxProjectedPct: 100}
	}
	if pf.Data.Error != "" && len(pf.Data.Windows) == 0 {
		return providerUrgency{tier: 1}
	}

	var maxPct float64
	var worstWindow string
	var runsOut time.Duration
	for _, w := range pf.Data.Windows {
		proj := forecast.Project(w.Utilization, w.ResetsAt, forecast.GuessWindowType(w.Name))
		if proj.ProjectedPct > maxPct {
			maxPct = proj.ProjectedPct
			worstWindow = w.Name
			runsOut = proj.RunsOutIn
		}
	}

	tier := 4
	switch {
	case maxPct >= 100:
		tier = 2
	case maxPct >= 90:
		tier = 3
	}

	return providerUrgency{tier: tier, maxProjectedPct: maxPct, worstWindow: worstWindow, runsOutIn: runsOut}
}

// sortProvidersByUrgency sorts providers so the most urgent appear first.
func sortProvidersByUrgency(providers []ProviderFormatter) {
	sort.SliceStable(providers, func(i, j int) bool {
		ui := classifyProvider(&providers[i])
		uj := classifyProvider(&providers[j])
		if ui.tier != uj.tier {
			return ui.tier < uj.tier
		}
		// Within same tier, higher projected usage first
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
		if u.tier < worstU.tier || (u.tier == worstU.tier && u.maxProjectedPct > worstU.maxProjectedPct) {
			worstIdx = i
			worstU = u
		}
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

		line = fmt.Sprintf("⚠ %s at %.0f%%%s", windowLabel, worstU.maxProjectedPct, etaStr)
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
			fmt.Println(`{"error": "no providers configured"}`)
		} else {
			fmt.Println("no providers configured")
		}
		return
	}

	// Sort providers by urgency (most urgent first)
	sortProvidersByUrgency(output.Providers)

	if jsonMode {
		output.PrintJSON(cacheEntry)
	} else if plainMode || !isTTY() {
		printSummary(output, false)
		output.PrintPlain()
	} else {
		printSummary(output, true)
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
