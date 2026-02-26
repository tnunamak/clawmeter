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
	"github.com/tnunamak/clawmeter/internal/autostart"
	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/forecast"
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
	systray.SetIcon(icons.Gray)
	systray.SetTitle("clawmeter")
	systray.SetTooltip("Claude usage monitor — loading...")

	mHeader := systray.AddMenuItem("Claude Max", "")
	mHeader.Disable()
	systray.AddSeparator()
	mStatus := systray.AddMenuItem("Loading...", "")
	mStatus.Disable()
	mFive := systray.AddMenuItem("", "")
	mFive.Disable()
	mFive.Hide()
	mSeven := systray.AddMenuItem("", "")
	mSeven.Disable()
	mSeven.Hide()
	systray.AddSeparator()
	mReauth := systray.AddMenuItem("Open Claude Code to reauth", "")
	mReauth.Hide()
	mRefresh := systray.AddMenuItem("Refresh Now", "")
	systray.AddSeparator()
	mAutostart := systray.AddMenuItem("", "")
	updateAutostartLabel(mAutostart)
	mQuit := systray.AddMenuItem("Quit", "")

	setExpired := func() {
		systray.SetIcon(icons.Gray)
		systray.SetTitle("expired")
		systray.SetTooltip("Claude — token expired")
		mStatus.SetTitle("Token expired")
		mStatus.Show()
		mFive.Hide()
		mSeven.Hide()
		mReauth.Show()
	}

	setUsage := func(usage *api.UsageResponse) {
		mStatus.Hide()
		mReauth.Hide()
		mFive.Show()
		mSeven.Show()
		updateMenu(usage, mFive, mSeven)
		updateIcon(usage)
		updateTooltip(usage)
		checkThresholds(usage)
	}

	refresh := func() {
		creds, err := api.ReadCredentials()
		if err != nil || creds.IsExpired() {
			setExpired()
			return
		}
		usage, err := api.FetchUsage(creds.AccessToken())
		if err != nil {
			mStatus.SetTitle(fmt.Sprintf("Error: %v", err))
			mStatus.Show()
			return
		}
		_ = cache.Write(usage)
		setUsage(usage)
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

func updateMenu(usage *api.UsageResponse, mFive, mSeven *systray.MenuItem) {
	fivePct := usage.FiveHour.Utilization
	sevenPct := usage.SevenDay.Utilization
	fiveReset := formatDuration(time.Until(usage.FiveHour.ResetsAt))
	sevenReset := formatDuration(time.Until(usage.SevenDay.ResetsAt))
	fiveProj := forecast.Project(fivePct, usage.FiveHour.ResetsAt, forecast.FiveHourWindow)
	sevenProj := forecast.Project(sevenPct, usage.SevenDay.ResetsAt, forecast.SevenDayWindow)

	mFive.SetTitle(fmt.Sprintf("5h: %3.0f%%  resets %s  %s", fivePct, fiveReset, fiveProj.Indicator()))
	mSeven.SetTitle(fmt.Sprintf("7d: %3.0f%%  resets %s  %s", sevenPct, sevenReset, sevenProj.Indicator()))
	systray.SetTitle(fmt.Sprintf("5h:%.0f%% 7d:%.0f%%", fivePct, sevenPct))
}

func updateTooltip(usage *api.UsageResponse) {
	fivePct := usage.FiveHour.Utilization
	sevenPct := usage.SevenDay.Utilization
	fiveReset := formatDuration(time.Until(usage.FiveHour.ResetsAt))
	sevenReset := formatDuration(time.Until(usage.SevenDay.ResetsAt))
	fiveProj := forecast.Project(fivePct, usage.FiveHour.ResetsAt, forecast.FiveHourWindow)
	sevenProj := forecast.Project(sevenPct, usage.SevenDay.ResetsAt, forecast.SevenDayWindow)

	systray.SetTooltip(fmt.Sprintf(
		"Claude Max\n5h: %.0f%% — resets %s — %s\n7d: %.0f%% — resets %s — %s",
		fivePct, fiveReset, fiveProj.Indicator(),
		sevenPct, sevenReset, sevenProj.Indicator(),
	))
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
