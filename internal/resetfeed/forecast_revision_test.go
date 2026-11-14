package resetfeed

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestRevisionAnnouncementUsesObservedRate(t *testing.T) {
	for _, now := range []time.Time{time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), time.Now()} {
		observed := now.Add(-5 * time.Minute)
		data := codexReading(observed, window("5h", 40.16, 3*time.Hour, observed))
		w := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"openai": data}, MaxReadingAge: 10 * time.Minute}, now).Accounts[0].Windows[0]
		if w.Exhaustion.State != view.ExhaustionRunsOut {
			t.Fatalf("fixture did not establish early exhaustion: %+v", w)
		}
		if got := Visible(data, announcementAt(now, time.Hour, now), now, 10*time.Minute); got == nil {
			t.Errorf("announcement lost the observed rate at %v", now)
		}
	}
}

func TestRevisionAnnouncementUsesConfiguredAgeAllowance(t *testing.T) {
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	data := codexReading(now.Add(-15*time.Minute), window("5h", 100, 3*time.Hour, now))
	reading := announcementAt(now, time.Hour, now)
	if got := Visible(data, reading, now, 10*time.Minute); got != nil {
		t.Fatal("old reading qualified under shorter allowance")
	}
	if got := Visible(data, reading, now, 20*time.Minute); got == nil {
		t.Fatal("valid reading lost configured age allowance")
	}
	// A retained 100% is usable, but the shared view cannot call it out now, so
	// an announcement that rests on out-now does not bear on it.
	data.Stale = true
	if w := view.Build(view.Inputs{Results: map[string]*provider.UsageData{"source": data}, MaxReadingAge: 20 * time.Minute}, now).Accounts[0].Windows[0]; w.Exhaustion.State != view.ExhaustionUnknown {
		t.Fatalf("retained 100%% exhaustion = %s, want unknown", w.Exhaustion.State)
	}
	if got := Visible(data, reading, now, 20*time.Minute); got != nil {
		t.Fatal("retained 100% announced out now")
	}
}

func TestRevisionAnnouncementRejectsUnusableObservation(t *testing.T) {
	now := time.Now()
	for _, observed := range []time.Time{{}, now.Add(time.Second), now.Add(-11 * time.Minute)} {
		data := codexReading(observed, window("5h", 100, 3*time.Hour, now))
		if got := Visible(data, announcementAt(now, time.Hour, now), now, 10*time.Minute); got != nil {
			t.Errorf("unusable observation %v qualifies: %+v", observed, got)
		}
	}
	data := codexReading(now.Add(-45*time.Second), window("5h", 99.6, 3*time.Hour, now.Add(-45*time.Second)))
	if got := Visible(data, announcementAt(now, time.Hour, now), now, 10*time.Minute); got != nil {
		t.Errorf("passed estimated exhaustion qualifies: %+v", got)
	}
}
