package provider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResetAnnouncementSummaryStrings(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	today := time.Date(2026, 10, 22, 10, 0, 0, 0, time.Local)
	tomorrow := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	lastYear := time.Date(2025, 10, 23, 10, 0, 0, 0, time.Local)

	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"today", today, "Reset announced for ~10:00 · codex-resets.com"},
		{"another day", tomorrow, "Reset announced for ~Oct 23 10:00 · codex-resets.com"},
		{"another year", lastYear, "Reset announced for ~Oct 23, 2025 10:00 · codex-resets.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &ResetAnnouncement{Feed: ResetAnnouncementFeed, ScheduledFor: c.at}
			if got := a.Summary(now); got != c.want {
				t.Fatalf("Summary = %q, want %q", got, c.want)
			}
		})
	}
	var none *ResetAnnouncement
	if none.Summary(now) != "" || none.AgentField() != "" {
		t.Fatal("nil announcement must render nothing")
	}
}

func TestResetAnnouncementAgentField(t *testing.T) {
	scheduled := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	a := &ResetAnnouncement{Feed: ResetAnnouncementFeed, ScheduledFor: scheduled, Windows: []string{"5h", "7d"}}
	want := "announced_reset=[~" + scheduled.Format(time.RFC3339) + " unofficial source=codex-resets.com windows=5h,7d]"
	if got := a.AgentField(); got != want {
		t.Fatalf("AgentField = %q, want %q", got, want)
	}
}

func TestResetAnnouncementJSONKeepsOfficialFalse(t *testing.T) {
	a := ResetAnnouncement{
		Kind: ResetAnnouncementKind, Official: false, Feed: ResetAnnouncementFeed,
		SourceURL: "https://x.com/a/status/1", Approximate: true, Windows: []string{"7d"},
		AnnouncedAt:   time.Date(2026, 10, 22, 11, 30, 0, 0, time.UTC),
		ScheduledFor:  time.Date(2026, 10, 23, 17, 0, 0, 0, time.UTC),
		FeedCheckedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		ShownUntil:    time.Date(2026, 10, 23, 23, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(UsageData{Provider: "openai", AnnouncedReset: &a})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Announced map[string]any `json:"announced_reset"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "official", "feed", "source_url", "announced_at", "scheduled_for", "approximate", "windows", "feed_checked_at", "shown_until"} {
		if _, ok := got.Announced[key]; !ok {
			t.Errorf("announced_reset lacks %q: %s", key, raw)
		}
	}
	if got.Announced["official"] != false {
		t.Fatalf("official = %v, want false", got.Announced["official"])
	}
	if !strings.Contains(string(raw), `"announced_reset":{`) {
		t.Fatalf("field not nested under announced_reset: %s", raw)
	}
	var plain UsageData
	if err := json.Unmarshal([]byte(`{"provider":"openai"}`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.AnnouncedReset != nil {
		t.Fatal("absent field must stay nil")
	}
	noneRaw, _ := json.Marshal(UsageData{Provider: "openai"})
	if strings.Contains(string(noneRaw), "announced_reset") {
		t.Fatalf("nil announcement must be omitted: %s", noneRaw)
	}
}
