//go:build tray

package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

const (
	testMaxGap  = 10 * time.Minute
	testAccount = "org:account-a"
	otherAcct   = "org:account-b"
)

var (
	testReset = time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	testBase  = time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
)

// windowAt is a five-hour window at the given utilization, resetting at the given time.
func windowAt(util float64, resetsAt time.Time) provider.UsageWindow {
	return provider.UsageWindow{Name: "5h", Utilization: util, ResetsAt: resetsAt}
}

// readingAt is one fetched result for source, reporting a single window.
func readingAt(util float64, resetsAt time.Time) *provider.UsageData {
	return &provider.UsageData{Windows: []provider.UsageWindow{windowAt(util, resetsAt)}}
}

// observeReading feeds one reading of source under account into tr, taken at the tray's clock time at.
func observeReading(tr *exhaustionTracker, source, account string, util float64, resetsAt, at time.Time) {
	tr.observeFetched(map[string]*provider.UsageData{source: readingAt(util, resetsAt)}, nil,
		map[string]string{source: account}, at, testMaxGap)
}

func TestExhaustionReportsFirstFullAfterBelow(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))

	got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(10*time.Minute))
	if !ok || !got.Equal(testBase.Add(5*time.Minute)) {
		t.Fatalf("ranOutAt = %v, %t; want first full reading at %v", got, ok, testBase.Add(5*time.Minute))
	}
}

func TestExhaustionHasNoTimeWhenFirstSeenFull(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(100, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))

	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(10*time.Minute)); ok {
		t.Fatalf("ranOutAt = %v; want no time when the tray first saw the window full", got)
	}
}

func TestExhaustionHasNoTimeAcrossAGapLargerThanMaxGap(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(30*time.Minute))

	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(40*time.Minute)); ok {
		t.Fatalf("ranOutAt = %v; want no time across a gap of 30m with maxGap 10m", got)
	}
}

func TestExhaustionNewResetPeriodClearsTheRecord(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))
	if _, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(10*time.Minute)); !ok {
		t.Fatal("old period lost its transition before the reset moved")
	}

	// The period rolls over, and the window is full again on its first
	// reading. That is not a transition the tray saw.
	next := testReset.Add(5 * time.Hour)
	l.observe("claude", testAccount, windowAt(100, next), testBase.Add(5*time.Hour))
	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, next), testMaxGap, testBase.Add(6*time.Hour)); ok {
		t.Fatalf("ranOutAt in the new period = %v; want none", got)
	}
	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(6*time.Hour)); ok {
		t.Fatalf("ranOutAt for the old reset = %v; want none once the period moved on", got)
	}
}

func TestExhaustionResetJitterKeepsTheRecord(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset.Add(-3*time.Second)), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset.Add(2*time.Second)), testBase.Add(5*time.Minute))

	got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset.Add(-1*time.Second)), testMaxGap, testBase.Add(10*time.Minute))
	if !ok || !got.Equal(testBase.Add(5*time.Minute)) {
		t.Fatalf("ranOutAt with jittered resets = %v, %t; want the transition kept", got, ok)
	}
}

func TestExhaustionDropToBelowStartsAnotherTransition(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(100, testReset), testBase)
	l.observe("claude", testAccount, windowAt(60, testReset), testBase.Add(5*time.Minute))
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(10*time.Minute))

	got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(11*time.Minute))
	if !ok || !got.Equal(testBase.Add(10*time.Minute)) {
		t.Fatalf("ranOutAt after dropping below = %v, %t; want the second transition", got, ok)
	}
}

func TestExhaustionNoTimeWhileWindowIsNotFull(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))

	if got, ok := l.ranOutAt("claude", testAccount, windowAt(90, testReset), testMaxGap, testBase.Add(10*time.Minute)); ok {
		t.Fatalf("ranOutAt for a window now at 90%% = %v; want none", got)
	}
}

func TestExhaustionIgnoresWindowsWithoutAReset(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(100, time.Time{}), testBase)
	if len(l.Windows) != 0 {
		t.Fatalf("observed a window with no reset: %+v", l.Windows)
	}
	if _, ok := l.ranOutAt("claude", testAccount, windowAt(100, time.Time{}), testMaxGap, testBase); ok {
		t.Fatal("ranOutAt returned a time for a window with no reset")
	}
}

func TestExhaustionIgnoresReadingsWithoutAnAccount(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", "", windowAt(80, testReset), testBase)
	l.observe("claude", "", windowAt(100, testReset), testBase.Add(5*time.Minute))
	if len(l.Windows) != 0 {
		t.Fatalf("tracked a reading with no account: %+v", l.Windows)
	}
	if _, ok := l.ranOutAt("claude", "", windowAt(100, testReset), testMaxGap, testBase.Add(10*time.Minute)); ok {
		t.Fatal("ranOutAt answered for an empty account")
	}
}

func TestExhaustionAccountSwitchInheritsNoTime(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))

	// The source now reads another account whose window resets at the same
	// moment and is already full on its first reading.
	l.observe("claude", otherAcct, windowAt(100, testReset), testBase.Add(10*time.Minute))
	now := testBase.Add(11 * time.Minute)
	if got, ok := l.ranOutAt("claude", otherAcct, windowAt(100, testReset), testMaxGap, now); ok {
		t.Fatalf("new account inherited a time: %v", got)
	}
	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, now); ok {
		t.Fatalf("old account's time still answers after the switch: %v", got)
	}

	// The new account's own transition then works.
	l.observe("claude", otherAcct, windowAt(70, testReset), testBase.Add(12*time.Minute))
	l.observe("claude", otherAcct, windowAt(100, testReset), testBase.Add(13*time.Minute))
	got, ok := l.ranOutAt("claude", otherAcct, windowAt(100, testReset), testMaxGap, testBase.Add(14*time.Minute))
	if !ok || !got.Equal(testBase.Add(13*time.Minute)) {
		t.Fatalf("new account's own transition = %v, %t; want %v", got, ok, testBase.Add(13*time.Minute))
	}
}

func TestExhaustionRecordAnswersOnlyForItsAccount(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))
	if got, ok := l.ranOutAt("claude", otherAcct, windowAt(100, testReset), testMaxGap, testBase.Add(10*time.Minute)); ok {
		t.Fatalf("another account read the record's time %v", got)
	}
}

func TestExhaustionZeroGapIsRejected(t *testing.T) {
	l := exhaustionLog{Windows: map[string]exhaustionRecord{
		exhaustionKey("claude", "5h"): {
			Account: testAccount, ResetsAt: testReset,
			LastSeen: testBase, LastBelow: testBase, FirstFull: testBase,
		},
	}}
	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(time.Minute)); ok {
		t.Fatalf("zero gap produced a time %v", got)
	}
}

func TestExhaustionIgnoresReadingsNotAfterTheLastSeen(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))

	// An equal time and an earlier time must not rewrite the record.
	l.observe("claude", testAccount, windowAt(50, testReset), testBase.Add(5*time.Minute))
	l.observe("claude", testAccount, windowAt(50, testReset), testBase.Add(time.Minute))

	got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(10*time.Minute))
	if !ok || !got.Equal(testBase.Add(5*time.Minute)) {
		t.Fatalf("ranOutAt = %v, %t; want the transition kept after equal and reversed readings", got, ok)
	}
}

func TestExhaustionFutureTransitionIsNotShown(t *testing.T) {
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(80, testReset), testBase)
	l.observe("claude", testAccount, windowAt(100, testReset), testBase.Add(5*time.Minute))

	if got, ok := l.ranOutAt("claude", testAccount, windowAt(100, testReset), testMaxGap, testBase.Add(4*time.Minute)); ok {
		t.Fatalf("ranOutAt before the transition's time = %v; want none", got)
	}
}

func TestExhaustionPrunesPeriodsEndedMoreThanADayAgo(t *testing.T) {
	now := testBase
	var l exhaustionLog
	l.observe("claude", testAccount, windowAt(100, now.Add(-25*time.Hour)), now.Add(-26*time.Hour))
	l.observe("claude:dondo", testAccount, windowAt(100, now.Add(-23*time.Hour)), now.Add(-24*time.Hour))
	l.observe("codex", testAccount, windowAt(100, now.Add(time.Hour)), now.Add(-time.Minute))

	path := filepath.Join(t.TempDir(), "exhaustion.json")
	if err := l.save(path, now); err != nil {
		t.Fatal(err)
	}
	loaded := loadExhaustionLog(path)
	if _, ok := loaded.Windows[exhaustionKey("claude", "5h")]; ok {
		t.Fatal("period that ended 25h ago was kept")
	}
	for _, source := range []string{"claude:dondo", "codex"} {
		if _, ok := loaded.Windows[exhaustionKey(source, "5h")]; !ok {
			t.Fatalf("%s period was pruned; it ended less than a day ago or is current", source)
		}
	}
}

func TestExhaustionPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")

	var first exhaustionTracker
	first.load(path, testMaxGap)
	observeReading(&first, "claude", testAccount, 80, testReset, testBase)
	observeReading(&first, "claude", testAccount, 100, testReset, testBase.Add(5*time.Minute))

	var second exhaustionTracker
	second.load(path, testMaxGap)
	observeReading(&second, "claude", testAccount, 100, testReset, testBase.Add(6*time.Minute))
	if got := second.ranOutAt("claude", windowAt(100, testReset), testBase.Add(7*time.Minute)); !got.Equal(testBase.Add(5 * time.Minute)) {
		t.Fatalf("after restart ranOutAt = %v; want %v", got, testBase.Add(5*time.Minute))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("log mode = %v; want 0600", mode)
	}
}

func TestExhaustionRestartShowsNothingBeforeReobservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	var first exhaustionTracker
	first.load(path, testMaxGap)
	observeReading(&first, "claude", testAccount, 80, testReset, testBase)
	observeReading(&first, "claude", testAccount, 100, testReset, testBase.Add(5*time.Minute))

	var second exhaustionTracker
	second.load(path, testMaxGap)
	if got := second.ranOutAt("claude", windowAt(100, testReset), testBase.Add(time.Hour)); !got.IsZero() {
		t.Fatalf("loaded record shown before this run re-observed the source: %v", got)
	}
}

func TestExhaustionRestartWithChangedAccountShowsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	var first exhaustionTracker
	first.load(path, testMaxGap)
	observeReading(&first, "claude", testAccount, 80, testReset, testBase)
	observeReading(&first, "claude", testAccount, 100, testReset, testBase.Add(5*time.Minute))

	var second exhaustionTracker
	second.load(path, testMaxGap)
	observeReading(&second, "claude", otherAcct, 100, testReset, testBase.Add(time.Hour))
	if got := second.ranOutAt("claude", windowAt(100, testReset), testBase.Add(time.Hour+time.Minute)); !got.IsZero() {
		t.Fatalf("saved account's time shown for another account after restart: %v", got)
	}
}

func TestExhaustionCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	var tr exhaustionTracker
	tr.load(path, testMaxGap)
	observeReading(&tr, "claude", testAccount, 100, testReset, testBase)
	if got := tr.ranOutAt("claude", windowAt(100, testReset), testBase.Add(time.Minute)); !got.IsZero() {
		t.Fatalf("corrupt file produced ranOutAt = %v", got)
	}
	if loaded := loadExhaustionLog(filepath.Join(t.TempDir(), "missing.json")); len(loaded.Windows) != 0 {
		t.Fatalf("missing file loaded %d windows", len(loaded.Windows))
	}
}

func TestObserveFetchedOnlyCountsFreshReadings(t *testing.T) {
	at := testBase.Add(5 * time.Minute)
	fresh := func(util float64) *provider.UsageData {
		return &provider.UsageData{FetchedAt: testBase, Windows: []provider.UsageWindow{windowAt(util, testReset)}}
	}
	identities := map[string]string{"fresh": testAccount, "stale": testAccount, "errored": testAccount, "expired": testAccount, "cached": testAccount}

	var tr exhaustionTracker
	tr.observeFetched(map[string]*provider.UsageData{
		"fresh": fresh(80), "stale": fresh(80), "errored": fresh(80), "expired": fresh(80), "cached": fresh(80),
	}, nil, identities, testBase, testMaxGap)
	tr.observeFetched(map[string]*provider.UsageData{
		"fresh":   fresh(100),
		"stale":   {Stale: true, FetchedAt: at, Windows: []provider.UsageWindow{windowAt(100, testReset)}},
		"errored": {Error: "boom", FetchedAt: at, Windows: []provider.UsageWindow{windowAt(100, testReset)}},
		"expired": {IsExpired: true, FetchedAt: at, Windows: []provider.UsageWindow{windowAt(100, testReset)}},
		"cached":  fresh(100),
		"nothing": nil,
	}, map[string]*provider.UsageData{"cached": fresh(100)}, identities, at, testMaxGap)

	if got := tr.ranOutAt("fresh", windowAt(100, testReset), at.Add(time.Minute)); !got.Equal(at) {
		t.Fatalf("fresh reading: ranOutAt = %v; want %v", got, at)
	}
	for _, name := range []string{"stale", "errored", "expired", "cached"} {
		if got := tr.ranOutAt(name, windowAt(100, testReset), at.Add(time.Minute)); !got.IsZero() {
			t.Fatalf("%s reading counted as an observation: ranOutAt = %v", name, got)
		}
	}
}

func TestExhaustionTimesComeFromTheTraysClockNotFetchedAt(t *testing.T) {
	yesterday := testBase.Add(-24 * time.Hour)
	var tr exhaustionTracker
	tr.observeFetched(map[string]*provider.UsageData{"claude": {FetchedAt: yesterday, Windows: []provider.UsageWindow{windowAt(80, testReset)}}},
		nil, map[string]string{"claude": testAccount}, testBase, testMaxGap)
	tr.observeFetched(map[string]*provider.UsageData{"claude": {FetchedAt: yesterday, Windows: []provider.UsageWindow{windowAt(100, testReset)}}},
		nil, map[string]string{"claude": testAccount}, testBase.Add(5*time.Minute), testMaxGap)

	got := tr.ranOutAt("claude", windowAt(100, testReset), testBase.Add(6*time.Minute))
	if !got.Equal(testBase.Add(5 * time.Minute)) {
		t.Fatalf("ranOutAt = %v; want the tray's observation time %v, not the provider's", got, testBase.Add(5*time.Minute))
	}
}

func TestExhaustionSourceWithoutIdentityIsNeverTracked(t *testing.T) {
	var tr exhaustionTracker
	for _, identities := range []map[string]string{nil, {"claude": ""}} {
		tr.observeFetched(map[string]*provider.UsageData{"claude": readingAt(80, testReset)}, nil, identities, testBase, testMaxGap)
		tr.observeFetched(map[string]*provider.UsageData{"claude": readingAt(100, testReset)}, nil, identities, testBase.Add(5*time.Minute), testMaxGap)
		if got := tr.ranOutAt("claude", windowAt(100, testReset), testBase.Add(6*time.Minute)); !got.IsZero() {
			t.Fatalf("source with identity %v got a time %v", identities, got)
		}
	}
	if len(tr.log.Windows) != 0 {
		t.Fatalf("source without identity was observed: %+v", tr.log.Windows)
	}
}

func TestExhaustionAccountSwitchWithIdenticalResetInheritsNothingOnTheTray(t *testing.T) {
	var tr exhaustionTracker
	observeReading(&tr, "claude", testAccount, 80, testReset, testBase)
	observeReading(&tr, "claude", testAccount, 100, testReset, testBase.Add(5*time.Minute))

	observeReading(&tr, "claude", otherAcct, 100, testReset, testBase.Add(10*time.Minute))
	if got := tr.ranOutAt("claude", windowAt(100, testReset), testBase.Add(11*time.Minute)); !got.IsZero() {
		t.Fatalf("new account inherited the old time %v", got)
	}

	observeReading(&tr, "claude", otherAcct, 70, testReset, testBase.Add(12*time.Minute))
	observeReading(&tr, "claude", otherAcct, 100, testReset, testBase.Add(13*time.Minute))
	if got := tr.ranOutAt("claude", windowAt(100, testReset), testBase.Add(14*time.Minute)); !got.Equal(testBase.Add(13 * time.Minute)) {
		t.Fatalf("new account's own transition = %v; want %v", got, testBase.Add(13*time.Minute))
	}
}

// orgTestProvider is a Claude source that reports its organization.
type orgTestProvider struct {
	sourceMenuTestProvider
	org string
}

func (p orgTestProvider) ClaudeOrganizationUUID() string { return p.org }

func TestExhaustionSourceNotReadThisRunShowsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	var first exhaustionTracker
	first.load(path, testMaxGap)
	observeReading(&first, "claude", testAccount, 80, testReset, testBase)
	observeReading(&first, "claude", testAccount, 100, testReset, testBase.Add(5*time.Minute))

	// This run knows the account but did not read the source, as when it
	// is served from cache. The saved transition must not be shown.
	var second exhaustionTracker
	second.load(path, testMaxGap)
	second.observeFetched(map[string]*provider.UsageData{}, nil, map[string]string{"claude": testAccount}, testBase.Add(time.Hour), testMaxGap)
	if got := second.ranOutAt("claude", windowAt(100, testReset), testBase.Add(time.Hour+time.Minute)); !got.IsZero() {
		t.Fatalf("saved time shown for a source this run did not read: %v", got)
	}
}

func TestSourceIdentityHashesAndPrefersTheOrganization(t *testing.T) {
	withOrg := orgTestProvider{sourceMenuTestProvider{id: "default", revision: "rev-1"}, "11111111-2222-3333-4444-555555555555"}
	org := sourceIdentity(withOrg, "rev-1")
	if !strings.HasPrefix(org, "org:") || len(org) != len("org:")+16 || strings.Contains(org, "1111") {
		t.Fatalf("organization identity = %q; want org: plus 16 hex digits of a hash", org)
	}
	rev := sourceIdentity(sourceMenuTestProvider{id: "default", revision: "rev-1"}, "rev-1")
	if !strings.HasPrefix(rev, "rev:") || len(rev) != len("rev:")+16 {
		t.Fatalf("revision identity = %q; want rev: plus 16 hex digits", rev)
	}
	if rev == sourceIdentity(sourceMenuTestProvider{id: "default", revision: "rev-2"}, "rev-2") {
		t.Fatal("different revisions produced the same identity")
	}
	if got := sourceIdentity(sourceMenuTestProvider{id: "default"}, ""); got != "" {
		t.Fatalf("reading without a login revision = %q; want empty", got)
	}
	if got := sourceIdentity(withOrg, ""); got != "" {
		t.Fatalf("organization without a reading revision = %q; want empty", got)
	}
	ids := sourceIdentities([]provider.Provider{sourceMenuTestProvider{id: "default", revision: "rev-1"}, sourceMenuTestProvider{id: "odl"}},
		map[string]string{"claude": "rev-1"})
	if ids["claude"] != rev || len(ids) != 1 {
		t.Fatalf("sourceIdentities = %v; want only the source with an identity, keyed by source key", ids)
	}
}

func TestAReadingIsNotFiledUnderALoginThatReplacedIt(t *testing.T) {
	// The reading was taken with login rev-A; the source now holds rev-B.
	switched := sourceMenuTestProvider{id: "default", revision: "rev-B"}
	if got := sourceIdentity(switched, "rev-A"); got != "" {
		t.Fatalf("identity after a login change = %q; want empty", got)
	}
	withOrg := orgTestProvider{switched, "22222222-2222-3333-4444-555555555555"}
	if got := sourceIdentity(withOrg, "rev-A"); got != "" {
		t.Fatalf("organization identity after a login change = %q; want empty", got)
	}
	if ids := sourceIdentities([]provider.Provider{switched}, map[string]string{"claude": "rev-A"}); len(ids) != 0 {
		t.Fatalf("sourceIdentities = %v; want none", ids)
	}
}

func TestRunOutLabelsNameTheTimeTheTrayRecorded(t *testing.T) {
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 9, 40, 0, 0, time.Local)
	if got := ranOutLabel(at, today); got != "out since 09:40" {
		t.Fatalf("ranOutLabel today = %q", got)
	}
	if got := ranOutLabel(time.Time{}, today); got != "out now" {
		t.Fatalf("ranOutLabel without a time = %q", got)
	}
	yesterday := time.Date(2026, 10, 21, 9, 40, 0, 0, time.Local)
	if got := runOutSince(yesterday, time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)); got != "Oct 21 09:40" {
		t.Fatalf("runOutSince on an earlier day = %q", got)
	}
	if got := ranOutNote("out now (2h00m until reset)", at, today); got != "out since 09:40 (2h00m until reset)" {
		t.Fatalf("ranOutNote = %q", got)
	}
	if got := ranOutNote("runs out in 1h", at, today); got != "runs out in 1h" {
		t.Fatalf("ranOutNote changed a future run-out note: %q", got)
	}
}

func TestTrayRowShowsWhenAWindowRanOut(t *testing.T) {
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 9, 40, 0, 0, time.Local)
	row := meter.Row{Code: "5h", Used: "100%", RunsOut: "now", RanOutAt: at}
	widths := trayWidths{used: len("100%"), forecast: len("100%")}
	if got := trayRowText(row, widths, time.Now()); !strings.Contains(got, "out since 09:40") || strings.Contains(got, "out now") {
		t.Fatalf("row with a known time = %q", got)
	}
	row.RanOutAt = time.Time{}
	if got := trayRowText(row, widths, time.Now()); !strings.Contains(got, "out now") {
		t.Fatalf("row without a known time = %q", got)
	}
}

func TestTooltipShowsWhenAWindowRanOut(t *testing.T) {
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 9, 40, 0, 0, time.Local)
	window := provider.UsageWindow{Name: "7d", Length: 7 * 24 * time.Hour, Utilization: 100, ResetsAt: time.Now().Add(2 * time.Hour)}
	results := map[string]*provider.UsageData{"codex": {Windows: []provider.UsageWindow{window}}}
	names := map[string]string{"codex": "Codex"}
	tooltipWith := func(outSince time.Time) string {
		in := view.Inputs{Results: results, DisplayNames: names, Families: map[string]string{"codex": "codex"}, FamilySourceCounts: map[string]int{"codex": 1}}
		if !outSince.IsZero() {
			in.OutSince = map[string]map[string]time.Time{"codex": {"7d": outSince}}
		}
		return Tooltip(view.Build(in, time.Now()), results)
	}

	if got := tooltipWith(at); !strings.Contains(got, "Out since 09:40 (2h00m until reset)") {
		t.Fatalf("tooltip with a known time = %q", got)
	}
	if got := tooltipWith(time.Time{}); !strings.Contains(got, "Out now (2h00m until reset)") {
		t.Fatalf("tooltip without a known time = %q", got)
	}
}

func TestTrayMenuRowsUsesTheObservedTransition(t *testing.T) {
	windowExhaustion.load(filepath.Join(t.TempDir(), "exhaustion.json"), testMaxGap)
	t.Cleanup(func() { windowExhaustion.load("", 0) })

	now := time.Now()
	reset := now.Add(3 * time.Hour)
	before, after := now.Add(-10*time.Minute), now.Add(-5*time.Minute)
	full := func(at time.Time) map[string]*provider.UsageData {
		return map[string]*provider.UsageData{"a": {FetchedAt: at, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: reset}}}}
	}
	identities := map[string]string{"a": testAccount}
	windowExhaustion.observeFetched(map[string]*provider.UsageData{
		"a": {FetchedAt: before, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 80, ResetsAt: reset}}},
	}, nil, identities, before, testMaxGap)
	windowExhaustion.observeFetched(full(after), nil, identities, after, testMaxGap)

	rows := MenuRows(trayViewOf(full(after), testMenus("a"), now), maxWindowItems)
	want := "out since " + runOutSince(after, now)
	if len(rows["a"]) != 1 || !strings.Contains(rows["a"][0], want) {
		t.Fatalf("menu rows = %q; want %q", rows["a"], want)
	}
}

func TestTrayMenuRowsSaysOutNowUntilTheRunIsObserved(t *testing.T) {
	now := time.Now()
	reset := now.Add(3 * time.Hour)
	path := filepath.Join(t.TempDir(), "exhaustion.json")
	var saved exhaustionTracker
	saved.load(path, testMaxGap)
	observeReading(&saved, "a", testAccount, 80, reset, testBase)
	observeReading(&saved, "a", testAccount, 100, reset, testBase.Add(5*time.Minute))

	windowExhaustion.load(path, testMaxGap)
	t.Cleanup(func() { windowExhaustion.load("", 0) })

	results := map[string]*provider.UsageData{
		"a": {FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 100, ResetsAt: reset}}},
	}
	if rows := MenuRows(trayViewOf(results, testMenus("a"), now), maxWindowItems); !strings.Contains(rows["a"][0], "out now") {
		t.Fatalf("row before this run observed the source = %q; want out now", rows["a"][0])
	}
}

// switchingSource changes login while its organization is read.
type switchingSource struct {
	sourceMenuTestProvider
	revisions []string
	org       string
	onOrg     func(*switchingSource)
}

func (s *switchingSource) SourceRevision() string {
	r := s.revisions[0]
	if len(s.revisions) > 1 {
		s.revisions = s.revisions[1:]
	}
	return r
}

func (s *switchingSource) ClaudeOrganizationUUID() string { return s.org }

func TestAnOrganizationReadDuringALoginSwitchIsNotUsed(t *testing.T) {
	// The reading used rev-A. The login switches to B before the
	// organization is read and is restored afterwards; the restore writes
	// the file again, so its revision is new.
	s := &switchingSource{sourceMenuTestProvider: sourceMenuTestProvider{id: "default"}, revisions: []string{"rev-A", "rev-A2"}, org: "org-B"}
	if got := sourceIdentity(s, "rev-A"); got != "" {
		t.Fatalf("identity = %q; want none when the login changed around the organization read", got)
	}
	s = &switchingSource{sourceMenuTestProvider: sourceMenuTestProvider{id: "default"}, revisions: []string{"rev-B"}, org: "org-B"}
	if got := sourceIdentity(s, "rev-A"); got != "" {
		t.Fatalf("identity = %q; want none when the login changed before the organization read", got)
	}
}
