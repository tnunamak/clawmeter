//go:build tray

package tray

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/tnunamak/clawmeter/internal/api"
	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

const pollInterval = 5 * time.Minute

type state struct {
	mu           sync.Mutex
	lastFiveHour float64
	lastSevenDay float64
}

var s state

func Run() int {
	systray.Run(onReady, func() {})
	return 0
}

func onReady() {
	systray.SetTitle("clawmeter")
	systray.SetTooltip("Claude usage monitor")

	mHeader := systray.AddMenuItem("Claude Max", "")
	mHeader.Disable()
	systray.AddSeparator()
	mFive := systray.AddMenuItem("5h:  --%", "")
	mFive.Disable()
	mSeven := systray.AddMenuItem("7d:  --%", "")
	mSeven.Disable()
	systray.AddSeparator()
	mRefresh := systray.AddMenuItem("Refresh Now", "")
	mQuit := systray.AddMenuItem("Quit", "")

	refresh := func() {
		usage := fetchUsage()
		if usage == nil {
			return
		}
		updateMenu(usage, mFive, mSeven)
		updateIcon(usage)
		checkThresholds(usage)
	}

	refresh()

	ticker := time.NewTicker(pollInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				refresh()
			case <-mRefresh.ClickedCh:
				refresh()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func fetchUsage() *api.UsageResponse {
	creds, err := api.ReadCredentials()
	if err != nil || creds.IsExpired() {
		return nil
	}
	usage, err := api.FetchUsage(creds.AccessToken())
	if err != nil {
		return nil
	}
	_ = cache.Write(usage)
	return usage
}

func updateMenu(usage *api.UsageResponse, mFive, mSeven *systray.MenuItem) {
	fivePct := usage.FiveHour.Utilization
	sevenPct := usage.SevenDay.Utilization
	fiveReset := formatDuration(time.Until(usage.FiveHour.ResetsAt))
	sevenReset := formatDuration(time.Until(usage.SevenDay.ResetsAt))

	mFive.SetTitle(fmt.Sprintf("5h: %3.0f%%  resets %s", fivePct, fiveReset))
	mSeven.SetTitle(fmt.Sprintf("7d: %3.0f%%  resets %s", sevenPct, sevenReset))
	systray.SetTitle(fmt.Sprintf("5h:%.0f%% 7d:%.0f%%", fivePct, sevenPct))
}

func updateIcon(usage *api.UsageResponse) {
	pct := usage.FiveHour.Utilization
	if usage.SevenDay.Utilization > pct {
		pct = usage.SevenDay.Utilization
	}
	switch {
	case pct >= 80:
		systray.SetIcon(icons.Red)
	case pct >= 60:
		systray.SetIcon(icons.Yellow)
	default:
		systray.SetIcon(icons.Green)
	}
}

func checkThresholds(usage *api.UsageResponse) {
	pct := usage.FiveHour.Utilization
	if usage.SevenDay.Utilization > pct {
		pct = usage.SevenDay.Utilization
	}

	s.mu.Lock()
	prevPct := s.lastFiveHour
	if s.lastSevenDay > prevPct {
		prevPct = s.lastSevenDay
	}
	s.lastFiveHour = usage.FiveHour.Utilization
	s.lastSevenDay = usage.SevenDay.Utilization
	s.mu.Unlock()

	if pct >= 95 && prevPct < 95 {
		notify("Claude usage critical", fmt.Sprintf("Usage at %.0f%% — you may be rate limited soon", pct), "critical")
	} else if pct >= 80 && prevPct < 80 {
		notify("Claude usage warning", fmt.Sprintf("Usage at %.0f%%", pct), "normal")
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
