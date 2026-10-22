package resetfeed

import (
	"reflect"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// codexReading is a fresh Codex reading with the given windows.
func codexReading(now time.Time, windows ...provider.UsageWindow) *provider.UsageData {
	return &provider.UsageData{Provider: "openai", SourceID: "default", FetchedAt: now, Windows: windows}
}

func window(name string, util float64, resetsIn time.Duration, now time.Time) provider.UsageWindow {
	length := 7 * 24 * time.Hour
	if name == "5h" {
		length = 5 * time.Hour
	}
	return provider.UsageWindow{Name: name, Utilization: util, ResetsAt: now.Add(resetsIn), Length: length}
}

// announcementAt is a feed reading whose scheduled reset is at now+offset and
// was read successfully at checkedAt.
func announcementAt(now time.Time, offset time.Duration, checkedAt time.Time) Reading {
	return Reading{
		CheckedAt: checkedAt,
		Announcement: &Announcement{
			ID:           "evt_1",
			ScheduledFor: now.Add(offset),
			AnnouncedAt:  now.Add(offset - 12*time.Hour),
			SourceURL:    goodSource,
		},
	}
}

func TestBlockedWindowWithLaterNaturalResetIsShown(t *testing.T) {
	now := time.Now()
	data := codexReading(now, window("7d", 100, 3*24*time.Hour, now))
	r := announcementAt(now, time.Hour, now)

	got := Visible(data, r, now)
	if got == nil {
		t.Fatal("out-of-quota window with a later natural reset: want the announcement")
	}
	if !reflect.DeepEqual(got.Windows, []string{"7d"}) {
		t.Fatalf("windows = %v, want [7d]", got.Windows)
	}
}

func TestNaturalResetBeforeAnnouncedTimeIsHidden(t *testing.T) {
	now := time.Now()
	data := codexReading(now, window("5h", 100, 30*time.Minute, now))
	r := announcementAt(now, 2*time.Hour, now)

	if got := Visible(data, r, now); got != nil {
		t.Fatalf("window resets before the announced time: got %+v, want nil", got)
	}
}

func TestWindowWithRoomIsHidden(t *testing.T) {
	now := time.Now()
	// 20% after 4 of 7 days projects to 35% at reset: it does not run out.
	data := codexReading(now, window("7d", 20, 3*24*time.Hour, now))
	r := announcementAt(now, time.Hour, now)

	if got := Visible(data, r, now); got != nil {
		t.Fatalf("window with room: got %+v, want nil", got)
	}
}

func TestProjectedRunOutBeforeResetBears(t *testing.T) {
	now := time.Now()
	// 60% after 4 of 7 days runs out in about 2.7 days, before the 3-day reset.
	data := codexReading(now, window("7d", 60, 3*24*time.Hour, now))
	r := announcementAt(now, time.Hour, now)

	got := Visible(data, r, now)
	if got == nil || !reflect.DeepEqual(got.Windows, []string{"7d"}) {
		t.Fatalf("projected run-out before reset: got %+v, want the announcement for 7d", got)
	}
}

func TestStaleOrErroredReadingIsHidden(t *testing.T) {
	now := time.Now()
	blocked := window("7d", 100, 3*24*time.Hour, now)
	r := announcementAt(now, time.Hour, now)

	cases := map[string]*provider.UsageData{
		"stale":   {Provider: "openai", FetchedAt: now.Add(-time.Hour), Stale: true, Warning: "rate limited", Windows: []provider.UsageWindow{blocked}},
		"errored": {Provider: "openai", Error: "usage request failed (500)", Windows: []provider.UsageWindow{blocked}},
		"expired": {Provider: "openai", IsExpired: true, Error: "token expired", Windows: []provider.UsageWindow{blocked}},
	}
	for name, data := range cases {
		if got := Visible(data, r, now); got != nil {
			t.Errorf("%s reading: got %+v, want nil", name, got)
		}
	}
}

func TestOneWindowRecoversAndTheOtherStillBears(t *testing.T) {
	now := time.Now()
	// The 5h window resets before the announced time and refills; the 7d
	// window is still blocked past it. The line stays and names only 7d.
	data := codexReading(now,
		window("5h", 100, 30*time.Minute, now),
		window("7d", 100, 3*24*time.Hour, now),
	)
	r := announcementAt(now, 2*time.Hour, now)

	got := Visible(data, r, now)
	if got == nil {
		t.Fatal("one window still bears: want the announcement")
	}
	if !reflect.DeepEqual(got.Windows, []string{"7d"}) {
		t.Fatalf("windows = %v, want [7d]", got.Windows)
	}
}

func TestScheduledTimeRules(t *testing.T) {
	now := time.Now()
	blocked := []provider.UsageWindow{window("7d", 100, 3*24*time.Hour, now)}
	cases := []struct {
		name   string
		offset time.Duration
		want   bool
	}{
		{"upcoming", 3 * time.Hour, true},
		{"overdue under six hours", -5 * time.Hour, true},
		{"overdue over six hours", -7 * time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Visible(codexReading(now, blocked...), announcementAt(now, c.offset, now), now)
			if (got != nil) != c.want {
				t.Fatalf("visible = %v, want %v (%+v)", got != nil, c.want, got)
			}
			if got != nil && !got.ShownUntil.Equal(now.Add(c.offset).Add(6*time.Hour)) {
				t.Fatalf("shown_until = %v, want scheduled_for+6h", got.ShownUntil)
			}
		})
	}
}

func TestFeedCheckedWithinTwoHours(t *testing.T) {
	now := time.Now()
	blocked := []provider.UsageWindow{window("7d", 100, 3*24*time.Hour, now)}
	if got := Visible(codexReading(now, blocked...), announcementAt(now, time.Hour, now.Add(-119*time.Minute)), now); got == nil {
		t.Fatal("feed read 1h59m ago: want the announcement")
	}
	if got := Visible(codexReading(now, blocked...), announcementAt(now, time.Hour, now.Add(-2*time.Hour-time.Minute)), now); got != nil {
		t.Fatalf("feed read 2h01m ago: got %+v, want nil", got)
	}
}

func TestOnlyCodexSourcesAreAnnounced(t *testing.T) {
	now := time.Now()
	blocked := window("7d", 100, 3*24*time.Hour, now)
	data := &provider.UsageData{Provider: "claude", SourceID: "default", FetchedAt: now, Windows: []provider.UsageWindow{blocked}}
	if got := Visible(data, announcementAt(now, time.Hour, now), now); got != nil {
		t.Fatalf("claude source: got %+v, want nil", got)
	}
}

func TestAccountsDecideIndependently(t *testing.T) {
	now := time.Now()
	r := announcementAt(now, time.Hour, now)
	results := map[string]*provider.UsageData{
		"openai":      codexReading(now, window("7d", 100, 3*24*time.Hour, now)),
		"openai:work": codexReading(now, window("7d", 20, 3*24*time.Hour, now)),
	}
	results["openai:work"].SourceID = "work"

	out := AnnounceAll(results, r, now)
	if out["openai"].AnnouncedReset == nil {
		t.Fatal("blocked account: want its announcement")
	}
	if out["openai:work"].AnnouncedReset != nil {
		t.Fatalf("account with room: got %+v, want nil", out["openai:work"].AnnouncedReset)
	}
	if results["openai"].AnnouncedReset != nil {
		t.Fatal("AnnounceAll must not mutate its input")
	}
}

func TestAnnouncementFieldsAndNoQuotaChange(t *testing.T) {
	now := time.Now()
	windows := []provider.UsageWindow{window("7d", 100, 3*24*time.Hour, now)}
	data := codexReading(now, windows...)
	before := data.Clone()
	r := announcementAt(now, time.Hour, now.Add(-10*time.Minute))

	got := Announce(data, r, now)
	if got.AnnouncedReset == nil {
		t.Fatal("want an announcement")
	}
	a := got.AnnouncedReset
	if a.Kind != "third_party_announcement" || a.Official || a.Feed != "codex-resets.com" || !a.Approximate {
		t.Fatalf("labels = %+v", a)
	}
	if a.SourceURL != goodSource || !a.FeedCheckedAt.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("source/checked = %+v", a)
	}
	if !reflect.DeepEqual(got.Windows, before.Windows) || got.FetchedAt != before.FetchedAt || got.Error != before.Error {
		t.Fatal("announcement changed quota data")
	}
	if data.AnnouncedReset != nil {
		t.Fatal("Announce must return a copy, not mutate the input")
	}
	if Announce(data, Reading{}, now) != data {
		t.Fatal("no announcement: want the input unchanged")
	}
}

func TestAnnounceAllKeepsNilNil(t *testing.T) {
	if got := AnnounceAll(nil, Reading{}, time.Now()); got != nil {
		t.Fatalf("AnnounceAll(nil) = %v, want nil", got)
	}
}

// After the announced time, a window that is only projected to run out is not
// blocked by anything the announcement could change. This is what a Codex
// account looks like once a global reset has arrived: low use, a new window,
// a fast pace.
func TestAPassedAnnouncementIsHiddenForAWindowWithRoom(t *testing.T) {
	now := time.Now()
	data := codexReading(now, window("7d", 60, 5*24*time.Hour, now))
	r := announcementAt(now, -4*time.Hour, now)
	if got := Visible(data, r, now); got != nil {
		t.Fatalf("passed announcement, window with room: got %+v, want nil", got)
	}
}

func TestAPassedAnnouncementIsShownWhileTheWindowIsStillOut(t *testing.T) {
	now := time.Now()
	data := codexReading(now, window("7d", 100, 3*24*time.Hour, now))
	r := announcementAt(now, -time.Hour, now)
	got := Visible(data, r, now)
	if got == nil || !reflect.DeepEqual(got.Windows, []string{"7d"}) {
		t.Fatalf("passed announcement, window still out: got %+v, want it for 7d", got)
	}
}

func TestAReadingFromBeforeAPassedAnnouncementDoesNotShowIt(t *testing.T) {
	now := time.Now()
	data := codexReading(now.Add(-2*time.Hour), window("7d", 100, 3*24*time.Hour, now))
	r := announcementAt(now, -time.Hour, now)
	if got := Visible(data, r, now); got != nil {
		t.Fatalf("reading from before the announced time: got %+v, want nil", got)
	}
}
