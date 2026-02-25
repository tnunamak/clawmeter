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

	fmt.Printf("clawmeter  5h %s%s%s %3.0f%%  resets %s\n",
		color(fivePct), bar(fivePct), reset, fivePct, fiveReset)
	fmt.Printf("           7d %s%s%s %3.0f%%  resets %s\n",
		color(sevenPct), bar(sevenPct), reset, sevenPct, sevenReset)
}

func PrintPlain(usage *api.UsageResponse) {
	fivePct := usage.FiveHour.Utilization
	sevenPct := usage.SevenDay.Utilization
	fiveReset := formatDuration(time.Until(usage.FiveHour.ResetsAt))
	sevenReset := formatDuration(time.Until(usage.SevenDay.ResetsAt))

	fmt.Printf("5h: %.0f%% (resets %s)  7d: %.0f%% (resets %s)\n",
		fivePct, fiveReset, sevenPct, sevenReset)
}

type JSONOutput struct {
	Usage  *api.UsageResponse `json:"usage"`
	Cache  *CacheInfo         `json:"cache,omitempty"`
}

type CacheInfo struct {
	Hit       bool      `json:"hit"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

func PrintJSON(usage *api.UsageResponse, cacheEntry *cache.Entry) {
	out := JSONOutput{Usage: usage}
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

	usage, err := api.FetchUsage(creds.ClaudeAiOauth.AccessToken)
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
