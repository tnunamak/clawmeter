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
			got := poolWithState(saved, tc.now, tc.state)
			if got.Live != tc.live || got.SignedOut != tc.signedOut || got.Balance != 100 {
				t.Fatalf("pool = %+v", got)
			}
		})
	}
	p := New(nil)
	before := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed, claudeweb.ExtensionState{})
	stale := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed.Add(6*time.Minute), claudeweb.ExtensionState{})
	out := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed, claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Second)})
	if before == stale || before == out || stale == out {
		t.Fatal("freshness or signed-out change did not invalidate the revision")
	}
	saved.ObservedAt = observed.Add(time.Second)
	if after := p.sourceRevision([]claudeweb.APICreditPool{saved}, observed.Add(time.Second), claudeweb.ExtensionState{}); after == before {
		t.Fatal("a saved poll did not invalidate the revision")
	}
	for _, source := range []string{"", "bookmark"} {
		saved.Source = source
		if got := poolWithState(saved, observed, claudeweb.ExtensionState{Status: "signed_out", At: observed.Add(time.Second)}); got.Live || got.SignedOut || !strings.Contains(got.Summary(observed), "checked ") {
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
	older, newer := poolWithState(a, now, state), poolWithState(b, now, state)
	if older.Live || !older.SignedOut || !strings.Contains(older.Summary(now), "sign in to the Claude Console") {
		t.Fatalf("A revived: %+v", older)
	}
	if !newer.Live || newer.SignedOut {
		t.Fatalf("B not live: %+v", newer)
	}
}
