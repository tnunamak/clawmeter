//go:build tray

package tray

import (
	"reflect"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestAWindowSeenOutRecoversWhenTheNextReadingHasRoom(t *testing.T) {
	var l exhaustionLog
	l.observe("codex", testAccount, windowAt(100, testReset), testBase, testMaxGap)
	if !l.observe("codex", testAccount, windowAt(4, testReset.Add(7*24*time.Hour)), testBase.Add(5*time.Minute), testMaxGap) {
		t.Fatal("a full window followed by a new period with room did not recover")
	}
	var same exhaustionLog
	same.observe("codex", testAccount, windowAt(100, testReset), testBase, testMaxGap)
	if !same.observe("codex", testAccount, windowAt(40, testReset), testBase.Add(5*time.Minute), testMaxGap) {
		t.Fatal("a full window that dropped below 100% in the same period did not recover")
	}
}

func TestOnlyARecentBlockedEpisodeOfTheSameAccountRecovers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   float64
		account string
		gap     time.Duration
	}{
		{"first reading only had room", 80, testAccount, 5 * time.Minute},
		{"readings too far apart, such as after a restart", 100, testAccount, testMaxGap + time.Minute},
		{"another account now", 100, otherAcct, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var l exhaustionLog
			l.observe("codex", testAccount, windowAt(tc.first, testReset), testBase, testMaxGap)
			if l.observe("codex", tc.account, windowAt(10, testReset), testBase.Add(tc.gap), testMaxGap) {
				t.Fatal("recovered; want a new baseline")
			}
		})
	}
}

func TestAnAccountIsAvailableOnlyWhenNoWindowIsStillOut(t *testing.T) {
	var tr exhaustionTracker
	ids := map[string]string{"codex": testAccount}
	week := testReset.Add(5 * 24 * time.Hour)
	reading := func(fiveHour, weekly float64) map[string]*provider.UsageData {
		return map[string]*provider.UsageData{"codex": {Windows: []provider.UsageWindow{
			{Name: "5h", Utilization: fiveHour, ResetsAt: testReset},
			{Name: "7d", Utilization: weekly, ResetsAt: week},
		}}}
	}
	tr.observeFetched(reading(100, 100), nil, ids, testBase, testMaxGap)
	if got := tr.observeFetched(reading(10, 100), nil, ids, testBase.Add(5*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("available = %v while 7d is still out", got)
	}
	if got := tr.observeFetched(reading(12, 3), nil, ids, testBase.Add(10*time.Minute), testMaxGap); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Fatalf("available = %v, want [codex] once every window has room", got)
	}
	if got := tr.observeFetched(reading(13, 4), nil, ids, testBase.Add(15*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("available = %v on the next reading; want one notice per episode", got)
	}
}

func TestAStaleReadingNeverMakesAnAccountAvailable(t *testing.T) {
	var tr exhaustionTracker
	ids := map[string]string{"codex": testAccount}
	tr.observeFetched(map[string]*provider.UsageData{"codex": readingAt(100, testReset)}, nil, ids, testBase, testMaxGap)
	stale := readingAt(10, testReset)
	stale.Stale = true
	if got := tr.observeFetched(map[string]*provider.UsageData{"codex": stale}, nil, ids, testBase.Add(5*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("available = %v from a stale reading", got)
	}
}

func TestQuotaAvailableNoticeStatesTheReadings(t *testing.T) {
	at := time.Date(2026, 10, 22, 10, 12, 0, 0, time.Local)
	results := map[string]*provider.UsageData{"codex": {Windows: []provider.UsageWindow{
		{Name: "5h", Utilization: 0, ResetsAt: at.Add(5 * time.Hour)},
		{Name: "7d", Utilization: 4, ResetsAt: at.Add(7 * 24 * time.Hour)},
	}}}
	menus := map[string]*providerMenuItems{"codex": {displayName: "Codex"}}
	title, body := quotaAvailableNotice([]string{"codex"}, results, menus, at)
	if title != "Codex quota available again" || body != "Codex: 5h 0% used, 7d 4% used\nChecked 10:12" {
		t.Fatalf("notice = %q / %q", title, body)
	}
	results["claude:odl"] = &provider.UsageData{Windows: []provider.UsageWindow{{Name: "7d All", Utilization: 1, ResetsAt: at.Add(time.Hour)}}}
	menus["claude:odl"] = &providerMenuItems{displayName: "Claude · ODL"}
	if title, _ := quotaAvailableNotice([]string{"claude:odl", "codex"}, results, menus, at); title != "Quota available again" {
		t.Fatalf("two accounts: title = %q", title)
	}
}
