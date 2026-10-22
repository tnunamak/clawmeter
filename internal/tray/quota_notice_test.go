//go:build tray

package tray

import (
	"reflect"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// watchReading is one fresh reading of codex with the given windows.
func watchReading(windows ...provider.UsageWindow) map[string]*provider.UsageData {
	return map[string]*provider.UsageData{"codex": {Windows: windows}}
}

func win(name string, used float64) provider.UsageWindow {
	return provider.UsageWindow{Name: name, Utilization: used, ResetsAt: testReset}
}

var watchIDs = map[string]string{"codex": testAccount}

func TestARecoveryIsReportedOncePerEpisode(t *testing.T) {
	var r recoveryWatch
	r.observe(watchReading(win("5h", 100), win("7d", 100)), nil, watchIDs, testBase, testMaxGap)
	if got := r.observe(watchReading(win("5h", 10), win("7d", 100)), nil, watchIDs, testBase.Add(5*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v while 7d is still out", got)
	}
	if got := r.observe(watchReading(win("5h", 12), win("7d", 3)), nil, watchIDs, testBase.Add(10*time.Minute), testMaxGap); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Fatalf("recovered = %v, want [codex]", got)
	}
	if got := r.observe(watchReading(win("5h", 13), win("7d", 4)), nil, watchIDs, testBase.Add(15*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v again", got)
	}
}

func TestAWindowMissingFromAReadingKeepsTheEpisodeOpen(t *testing.T) {
	var r recoveryWatch
	r.observe(watchReading(win("5h", 100), win("7d", 100)), nil, watchIDs, testBase, testMaxGap)
	if got := r.observe(watchReading(win("5h", 10)), nil, watchIDs, testBase.Add(5*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v with 7d missing", got)
	}
	if got := r.observe(watchReading(win("5h", 10), win("7d", 10)), nil, watchIDs, testBase.Add(10*time.Minute), testMaxGap); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Fatalf("recovered = %v once 7d is back with room, want [codex]", got)
	}
}

func TestAWindowWithoutAResetTimeCountsToo(t *testing.T) {
	var r recoveryWatch
	daily := provider.UsageWindow{Name: "daily", Utilization: 100}
	r.observe(watchReading(win("5h", 100), daily), nil, watchIDs, testBase, testMaxGap)
	if got := r.observe(watchReading(win("5h", 5), daily), nil, watchIDs, testBase.Add(5*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v while the daily window is still out", got)
	}
	daily.Utilization = 20
	if got := r.observe(watchReading(win("5h", 5), daily), nil, watchIDs, testBase.Add(10*time.Minute), testMaxGap); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Fatalf("recovered = %v, want [codex]", got)
	}
}

func TestOnlyARecentEpisodeOfTheSameAccountRecovers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account string
		gap     time.Duration
	}{
		{"readings too far apart", testAccount, testMaxGap + time.Minute},
		{"another account now", otherAcct, 5 * time.Minute},
		{"a reading at the same time", testAccount, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r recoveryWatch
			r.observe(watchReading(win("5h", 100)), nil, watchIDs, testBase, testMaxGap)
			if got := r.observe(watchReading(win("5h", 10)), nil, map[string]string{"codex": tc.account}, testBase.Add(tc.gap), testMaxGap); len(got) != 0 {
				t.Fatalf("recovered = %v; want a baseline", got)
			}
		})
	}
}

func TestAFreshWatchSetsABaseline(t *testing.T) {
	var r recoveryWatch
	if got := r.observe(watchReading(win("5h", 10)), nil, watchIDs, testBase, testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v on the first reading", got)
	}
}

func TestStaleAndCachedReadingsAreIgnored(t *testing.T) {
	var r recoveryWatch
	r.observe(watchReading(win("5h", 100)), nil, watchIDs, testBase, testMaxGap)
	stale := watchReading(win("5h", 10))
	stale["codex"].Stale = true
	if got := r.observe(stale, nil, watchIDs, testBase.Add(5*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v from a stale reading", got)
	}
	cachedReading := watchReading(win("5h", 10))
	if got := r.observe(cachedReading, cachedReading, watchIDs, testBase.Add(6*time.Minute), testMaxGap); len(got) != 0 {
		t.Fatalf("recovered = %v from a cached reading", got)
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
