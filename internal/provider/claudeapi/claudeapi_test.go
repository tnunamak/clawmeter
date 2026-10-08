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
