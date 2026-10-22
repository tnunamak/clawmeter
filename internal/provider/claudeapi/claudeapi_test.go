package claudeapi

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
)

// A cached pool must not outlive its grant: the revision changes when a
// saved grant expires.
func TestSourceRevisionChangesWhenAGrantExpires(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
	now := time.Now()
	pool := claudeweb.APICreditPool{
		Pool: strings.Repeat("a", 64), Name: "Org", Currency: "USD", Balance: 100, MonthResetsAt: now.Add(24 * time.Hour), ObservedAt: now,
		Grants: []claudeweb.APICreditGrant{{Name: "API credit", Granted: 100, Remaining: 100, GrantedAt: now.Add(-time.Hour), ExpiresAt: now.Add(1500 * time.Millisecond)}},
	}
	if err := claudeweb.WriteAPICreditPool(pool); err != nil {
		t.Fatal(err)
	}
	p := New(nil)
	before := p.SourceRevision()
	time.Sleep(2 * time.Second)
	if after := p.SourceRevision(); after == before {
		t.Fatalf("revision stayed %q after the grant expired", before)
	}
}

func TestExtensionPoolFreshness(t *testing.T) {
	observed := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	saved := claudeweb.APICreditPool{
		Pool: strings.Repeat("a", 64), Name: "Org", Currency: "USD",
		Source: "extension", Balance: 100, ObservedAt: observed,
		MonthResetsAt: observed.Add(24 * time.Hour),
	}
	for _, tc := range []struct {
		name      string
		now       time.Time
		state     claudeweb.ExtensionState
		live      bool
		signedOut bool
	}{
		{"fresh", observed, claudeweb.ExtensionState{}, true, false},
		{"boundary", observed.Add(5 * time.Minute), claudeweb.ExtensionState{}, true, false},
		{"stale", observed.Add(5*time.Minute + time.Nanosecond), claudeweb.ExtensionState{}, false, false},
		{"signed out", observed.Add(time.Minute), claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Second)}, false, true},
		{"old sign out", observed.Add(time.Minute), claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(-time.Second)}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := poolWithState(saved, tc.now, tc.state, time.Time{})
			if got.Live != tc.live || got.SignedOut != tc.signedOut || got.Balance != 100 {
				t.Fatalf("pool = %+v", got)
			}
		})
	}
	p := New(nil)
	before := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed, claudeweb.ExtensionState{}, time.Time{})
	stale := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed.Add(6*time.Minute), claudeweb.ExtensionState{}, time.Time{})
	out := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed, claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Second)}, time.Time{})
	if before == stale || before == out || stale == out {
		t.Fatal("freshness or signed-out change did not invalidate the revision")
	}
	saved.ObservedAt = observed.Add(time.Second)
	if after := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed.Add(time.Second), claudeweb.ExtensionState{}, time.Time{}); after == before {
		t.Fatal("a saved poll did not invalidate the revision")
	}
	recent := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed, claudeweb.ExtensionState{}, observed)
	if recent == before || p.sourceRevision([]claudeweb.APICreditPool{saved}, observed.Add(4*time.Minute), claudeweb.ExtensionState{}, observed) == recent {
		t.Fatal("the extension contact window did not invalidate the revision")
	}
	for _, source := range []string{"", "bookmark"} {
		saved.Source = source
		if got := poolWithState(saved, observed, claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Second)}, time.Time{}); got.Live || got.SignedOut || !strings.Contains(got.Summary(observed), "checked ") {
			t.Fatalf("bookmark changed: %+v", got)
		}
	}
}

func TestSignOutIsOrderedPerPool(t *testing.T) {
	observed := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	state := claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Minute)}
	a := claudeweb.APICreditPool{Pool: strings.Repeat("a", 64), Name: "A", Currency: "USD", Source: "extension", Balance: 100, ObservedAt: observed, MonthResetsAt: observed.Add(24 * time.Hour)}
	b := a
	b.Pool, b.Name, b.ObservedAt = strings.Repeat("b", 64), "B", observed.Add(2*time.Minute)
	now := b.ObservedAt
	older, newer := poolWithState(a, now, state, time.Time{}), poolWithState(b, now, state, time.Time{})
	if older.Live || !older.SignedOut || !strings.Contains(older.Summary(now), "sign in to the Claude Console") {
		t.Fatalf("A revived: %+v", older)
	}
	if !newer.Live || newer.SignedOut {
		t.Fatalf("B not live: %+v", newer)
	}
}

func TestLinkedSourceCarriesItsNextCreditDate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
	next := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	if err := claudeweb.WriteSummary(claudeweb.GrantSummary{ObservedAt: time.Now(), ResetCreditsTarget: "claude:odl", MatchSalt: "s", MatchHash: "h", NextCreditAt: &next}); err != nil {
		t.Fatal(err)
	}
	if got := nextCreditAt("claude:odl"); !got.Equal(next) {
		t.Fatalf("nextCreditAt = %v, want %v", got, next)
	}
	if got := nextCreditAt("claude:spare"); !got.IsZero() {
		t.Fatalf("source without a reading = %v", got)
	}
	if got := nextCreditAt(""); !got.IsZero() {
		t.Fatalf("unlinked pool = %v", got)
	}
}

func TestPoolActionNamesTheStepThatClearsIt(t *testing.T) {
	observed := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	extension := claudeweb.APICreditPool{Pool: strings.Repeat("a", 64), Name: "Org", Currency: "USD", Source: "extension", Balance: 5753,
		ObservedAt: observed, MonthResetsAt: observed.Add(24 * time.Hour)}
	bookmark := extension
	bookmark.Source = "bookmark"
	stale := observed.Add(6 * time.Minute)
	at := observed.Local().Format("15:04")
	signedOut := claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Second)}
	for _, tc := range []struct {
		name    string
		saved   claudeweb.APICreditPool
		now     time.Time
		state   claudeweb.ExtensionState
		contact time.Time
		action  string
		line    string
	}{
		{"live", extension, observed, claudeweb.ExtensionState{}, observed, "", "$57.53 left"},
		{"stale, browser contacted recently", extension, stale, claudeweb.ExtensionState{}, stale.Add(-time.Minute), signInAction,
			"$57.53 left · not updated since " + at + " · sign in to the Claude Console"},
		{"stale, browser not contacted", extension, stale, claudeweb.ExtensionState{}, observed.Add(-time.Hour), updateAction,
			"$57.53 left · not updated since " + at + " · open your browser to update"},
		{"stale, contact just outside the window", extension, stale, claudeweb.ExtensionState{}, stale.Add(-3*time.Minute - time.Second), updateAction,
			"$57.53 left · not updated since " + at + " · open your browser to update"},
		{"signed out", extension, observed.Add(time.Minute), signedOut, time.Time{}, signInAction,
			"$57.53 left · sign in to the Claude Console"},
		{"bookmark, not expired", bookmark, observed, claudeweb.ExtensionState{}, time.Time{}, "", "$57.53 left · checked " + at},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := poolWithState(tc.saved, tc.now, tc.state, tc.contact)
			if got.Action != tc.action {
				t.Fatalf("Action = %q, want %q", got.Action, tc.action)
			}
			if line := got.TrayLine(tc.now); !strings.HasPrefix(line, tc.line) {
				t.Fatalf("TrayLine() = %q, want prefix %q", line, tc.line)
			}
		})
	}
	expired := bookmark
	expired.Grants = []claudeweb.APICreditGrant{{Remaining: 5753, ExpiresAt: observed.Add(-time.Minute)}}
	if got := poolWithState(expired, observed, claudeweb.ExtensionState{}, time.Time{}); got.Action != consoleAction || !strings.HasSuffix(got.TrayLine(observed), "after the last check · open the Claude Console") {
		t.Fatalf("expired bookmark = %q, action %q", got.TrayLine(observed), got.Action)
	}
}
