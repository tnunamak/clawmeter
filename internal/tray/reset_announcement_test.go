//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/resetfeed"
	"github.com/tnunamak/clawmeter/internal/systray"
)

// announcedCodex is a fresh blocked Codex reading that carries a visible
// announcement, as the refresh path attaches it.
func announcedCodex(t *testing.T, now time.Time) *provider.UsageData {
	t.Helper()
	data := &provider.UsageData{
		Provider: "openai", SourceID: "default", FetchedAt: now,
		Windows: []provider.UsageWindow{{Name: "7d", Utilization: 100, ResetsAt: now.Add(72 * time.Hour), Length: 7 * 24 * time.Hour}},
	}
	reading := resetfeed.Reading{
		CheckedAt: now,
		Announcement: &resetfeed.Announcement{
			ID:           "evt_1",
			ScheduledFor: now.Add(2 * time.Hour),
			AnnouncedAt:  now.Add(-time.Hour),
			SourceURL:    "https://x.com/example/status/1",
		},
	}
	out := resetfeed.Announce(data, reading, now)
	if out.AnnouncedReset == nil {
		t.Fatal("fixture: expected a visible announcement")
	}
	return out
}

func TestAnnouncementTooltipLineFollowsResetLine(t *testing.T) {
	now := time.Now()
	data := announcedCodex(t, now)
	data.ResetCredits = &provider.UsageResetCredits{AvailableCount: 1, EarliestExpiresAt: now.Add(9 * 24 * time.Hour), FetchedAt: now}

	tooltip := appendResetCreditTooltip("Codex: out", data)
	lines := strings.Split(tooltip, "\n")
	if len(lines) != 3 {
		t.Fatalf("tooltip lines = %q", lines)
	}
	if !strings.HasPrefix(lines[1], "Resets: 1") {
		t.Fatalf("second line = %q, want the reset-credit row", lines[1])
	}
	if want := data.AnnouncedReset.Summary(time.Now()); lines[2] != want {
		t.Fatalf("announcement line = %q, want %q", lines[2], want)
	}
}

func TestAnnouncementTooltipAbsentWithoutAnnouncement(t *testing.T) {
	data := &provider.UsageData{Provider: "openai", Windows: []provider.UsageWindow{{Name: "7d", Utilization: 10}}}
	if got := appendResetCreditTooltip("Codex: 10%", data); got != "Codex: 10%" {
		t.Fatalf("tooltip = %q, want unchanged", got)
	}
}

func TestAnnouncementRowShowsAndOpensSourcePost(t *testing.T) {
	defer systray.ResetMenu()
	now := time.Now()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "openai"}, false, nil, false)
	data := announcedCodex(t, now)

	trayRenderMu.Lock()
	updateAnnouncementRow(menu, data, time.Now())
	title, visible, target := menu.announceState.title, menu.announceState.visible, menu.announceURL
	trayRenderMu.Unlock()
	if !visible || title != data.AnnouncedReset.Summary(time.Now()) {
		t.Fatalf("row = %q visible=%v", title, visible)
	}
	if target != "https://x.com/example/status/1" {
		t.Fatalf("row target = %q", target)
	}

	var opened string
	openAnnouncement(menu, func(url string) { opened = url })
	if opened != "https://x.com/example/status/1" {
		t.Fatalf("click opened %q", opened)
	}

	trayRenderMu.Lock()
	updateAnnouncementRow(menu, &provider.UsageData{Provider: "openai"}, time.Now())
	hidden := !menu.announceState.visible
	trayRenderMu.Unlock()
	if !hidden {
		t.Fatal("row without an announcement must hide")
	}
	opened = ""
	openAnnouncement(menu, func(url string) { opened = url })
	if opened != "" {
		t.Fatalf("hidden row opened %q", opened)
	}
}

func TestTrayFeedReadNeedsOptInAndMakesNoRequest(t *testing.T) {
	previous := resetFeedClient
	resetFeedClient = func() (*resetfeed.Client, error) {
		t.Fatal("setting off must not build a feed client")
		return nil, nil
	}
	t.Cleanup(func() { resetFeedClient = previous })

	off := config.DefaultConfig()
	codex := []provider.Provider{sourceMenuTestProvider{name: "openai"}}
	if reading := refreshResetAnnouncements(off, codex); reading.Announcement != nil || !reading.CheckedAt.IsZero() {
		t.Fatalf("setting off read = %+v", reading)
	}

	on := config.DefaultConfig()
	on.Settings.CodexResetAnnouncements = true
	resetFeedClient = func() (*resetfeed.Client, error) {
		t.Fatal("no Codex source must not build a feed client")
		return nil, nil
	}
	claude := []provider.Provider{sourceMenuTestProvider{name: "claude"}}
	if reading := refreshResetAnnouncements(on, claude); reading.Announcement != nil {
		t.Fatalf("no Codex source read = %+v", reading)
	}
}

func TestTrayAnnouncementIsCleanOnStaleOrErroredReadings(t *testing.T) {
	now := time.Now()
	reading := resetfeed.Reading{CheckedAt: now, Announcement: &resetfeed.Announcement{ScheduledFor: now.Add(time.Hour), SourceURL: "https://x.com/a"}}
	blocked := []provider.UsageWindow{{Name: "7d", Utilization: 100, ResetsAt: now.Add(72 * time.Hour), Length: 7 * 24 * time.Hour}}
	stale := &provider.UsageData{Provider: "openai", Stale: true, Windows: blocked}
	errored := &provider.UsageData{Provider: "openai", Error: "boom", Windows: blocked}
	for name, data := range map[string]*provider.UsageData{"stale": stale, "errored": errored} {
		results := resetfeed.AnnounceAll(map[string]*provider.UsageData{"openai": data}, reading, now)
		if results["openai"].AnnouncedReset != nil {
			t.Fatalf("%s reading carries an announcement", name)
		}
		if got := appendResetCreditTooltip("Codex", results["openai"]); strings.Contains(got, "Reset announced") {
			t.Fatalf("%s reading shows the announcement: %q", name, got)
		}
	}
}

// Selecting the Codex icon target re-renders the tooltip from stored results.
// The announcement is added at render time, so it must survive that path.
func TestIconTargetSelectionKeepsTheAnnouncement(t *testing.T) {
	defer systray.ResetMenu()
	now := time.Now()
	stored := &provider.UsageData{
		Provider: "openai", SourceID: "default", FetchedAt: now,
		Windows: []provider.UsageWindow{{Name: "7d", Utilization: 100, ResetsAt: now.Add(72 * time.Hour), Length: 7 * 24 * time.Hour}},
	}
	reading := resetfeed.Reading{
		CheckedAt:    now,
		Announcement: &resetfeed.Announcement{ID: "evt_1", ScheduledFor: now.Add(2 * time.Hour), AnnouncedAt: now.Add(-time.Hour), SourceURL: "https://x.com/example/status/1"},
	}

	s.mu.Lock()
	previousResults, previousReading := s.lastResults, s.feedReading
	previousOverride, previousTooltip := s.iconTargetOverride, s.currentTooltip
	s.lastResults = map[string]*provider.UsageData{"openai": stored}
	s.feedReading = reading
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		s.lastResults, s.feedReading = previousResults, previousReading
		s.iconTargetOverride, s.currentTooltip = previousOverride, previousTooltip
		s.mu.Unlock()
	})

	menus := map[string]*providerMenuItems{
		"openai": createProviderMenuItems(sourceMenuTestProvider{name: "openai"}, false, nil, false),
	}
	picker := newIconPicker(systray.AddMenuItem("Settings", ""))
	selectIconTarget(menus, picker, iconTarget{Provider: "openai", Window: "7d"})

	s.mu.Lock()
	tooltip := s.currentTooltip
	s.mu.Unlock()
	if !strings.Contains(tooltip, "\nReset announced for ~") {
		t.Fatalf("selecting the Codex target dropped the announcement; tooltip = %q", tooltip)
	}
}
