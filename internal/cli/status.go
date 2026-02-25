package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tnunamak/clawmeter/internal/api"
	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/forecast"
)

const barWidth = 20

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "now"
	}
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func color(pct float64) string {
	switch {
	case pct >= 80:
		return "\033[31m" // red
	case pct >= 60:
		return "\033[33m" // yellow
	default:
		return "\033[32m" // green
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

func PrintColor(usage *api.UsageResponse) {
	fivePct := usage.FiveHour.Utilization
	sevenPct := usage.SevenDay.Utilization
	fiveReset := formatDuration(time.Until(usage.FiveHour.ResetsAt))
	sevenReset := formatDuration(time.Until(usage.SevenDay.ResetsAt))
	fiveProj := forecast.Project(fivePct, usage.FiveHour.ResetsAt, forecast.FiveHourWindow)
	sevenProj := forecast.Project(sevenPct, usage.SevenDay.ResetsAt, forecast.SevenDayWindow)

	fmt.Printf("clawmeter  5h %s%s%s %3.0f%%  resets %s  %s\n",
		color(fivePct), bar(fivePct), reset, fivePct, fiveReset, fiveProj.ColorIndicator())
	fmt.Printf("           7d %s%s%s %3.0f%%  resets %s  %s\n",
		color(sevenPct), bar(sevenPct), reset, sevenPct, sevenReset, sevenProj.ColorIndicator())
}

func PrintPlain(usage *api.UsageResponse) {
	fivePct := usage.FiveHour.Utilization
	sevenPct := usage.SevenDay.Utilization
	fiveReset := formatDuration(time.Until(usage.FiveHour.ResetsAt))
	sevenReset := formatDuration(time.Until(usage.SevenDay.ResetsAt))
	fiveProj := forecast.Project(fivePct, usage.FiveHour.ResetsAt, forecast.FiveHourWindow)
	sevenProj := forecast.Project(sevenPct, usage.SevenDay.ResetsAt, forecast.SevenDayWindow)

	fmt.Printf("5h: %.0f%% (resets %s, %s)  7d: %.0f%% (resets %s, %s)\n",
		fivePct, fiveReset, fiveProj.Indicator(), sevenPct, sevenReset, sevenProj.Indicator())
}

type JSONOutput struct {
	Usage    *api.UsageResponse `json:"usage"`
	Forecast *JSONForecast      `json:"forecast"`
	Cache    *CacheInfo         `json:"cache,omitempty"`
}

type JSONForecast struct {
	FiveHour  JSONProjection `json:"five_hour"`
	SevenDay  JSONProjection `json:"seven_day"`
}

type JSONProjection struct {
	ProjectedPct float64 `json:"projected_pct"`
	Status       string  `json:"status"`
}

type CacheInfo struct {
	Hit       bool      `json:"hit"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

func PrintJSON(usage *api.UsageResponse, cacheEntry *cache.Entry) {
	fiveProj := forecast.Project(usage.FiveHour.Utilization, usage.FiveHour.ResetsAt, forecast.FiveHourWindow)
	sevenProj := forecast.Project(usage.SevenDay.Utilization, usage.SevenDay.ResetsAt, forecast.SevenDayWindow)

	out := JSONOutput{
		Usage: usage,
		Forecast: &JSONForecast{
			FiveHour:  JSONProjection{ProjectedPct: math.Round(fiveProj.ProjectedPct), Status: fiveProj.Indicator()},
			SevenDay:  JSONProjection{ProjectedPct: math.Round(sevenProj.ProjectedPct), Status: sevenProj.Indicator()},
		},
	}
	if cacheEntry != nil {
		out.Cache = &CacheInfo{Hit: true, FetchedAt: cacheEntry.FetchedAt}
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
}

func Status(jsonMode, plainMode bool) int {
	creds, err := api.ReadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 2
	}
	if creds.IsExpired() {
		fmt.Fprintln(os.Stderr, "clawmeter: token expired — open Claude Code to refresh")
		return 2
	}

	// Try cache first
	if entry, err := cache.Read(); err == nil && entry.IsValid() {
		if jsonMode {
			PrintJSON(entry.Usage, entry)
		} else if plainMode || !isTTY() {
			PrintPlain(entry.Usage)
		} else {
			PrintColor(entry.Usage)
		}
		return 0
	}

	usage, err := api.FetchUsage(creds.AccessToken())
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	_ = cache.Write(usage)

	if jsonMode {
		PrintJSON(usage, nil)
	} else if plainMode || !isTTY() {
		PrintPlain(usage)
	} else {
		PrintColor(usage)
	}
	return 0
}
