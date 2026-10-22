//go:build tray

package tray

import (
	"testing"
	"time"
)

func TestLoginWatchRefreshesOnceAfterALoginChanges(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var w loginWatch
	if w.due(map[string]string{"claude": "a", "openai": "x"}, nil, start) {
		t.Fatal("first check started a refresh")
	}
	if w.due(map[string]string{"claude": "a", "openai": "x"}, nil, start.Add(20*time.Second)) {
		t.Fatal("unchanged logins started a refresh")
	}
	at := start.Add(40 * time.Second)
	if !w.due(map[string]string{"claude": "b", "openai": "x"}, nil, at) {
		t.Fatal("a renewed login did not start a refresh")
	}
	w.ran(at)
	if w.due(map[string]string{"claude": "b", "openai": "x"}, nil, at.Add(20*time.Second)) {
		t.Fatal("one change started two refreshes")
	}
}

func TestLoginWatchRefreshesForANewLogin(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var w loginWatch
	w.due(map[string]string{"claude": "a"}, nil, start)
	if !w.due(map[string]string{"claude": "a", "claude:odl": "c"}, nil, start.Add(20*time.Second)) {
		t.Fatal("a new login did not start a refresh")
	}
	w.ran(start.Add(20 * time.Second))
	if w.due(map[string]string{"claude": "a"}, nil, start.Add(2*time.Minute)) {
		t.Fatal("a removed login started a refresh")
	}
}

func TestLoginWatchKeepsAChangeUntilARefreshRuns(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var w loginWatch
	w.due(map[string]string{"claude": "a"}, nil, start)
	if !w.due(map[string]string{"claude": "b"}, nil, start.Add(20*time.Second)) {
		t.Fatal("change not due")
	}
	// The refresh could not start because another one was running.
	if !w.due(map[string]string{"claude": "b"}, nil, start.Add(40*time.Second)) {
		t.Fatal("a change was dropped when its refresh could not start")
	}
}

func TestLoginWatchStartsAtMostOneRefreshAMinute(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var w loginWatch
	w.due(map[string]string{"claude": "a"}, nil, start)
	at := start.Add(20 * time.Second)
	if !w.due(map[string]string{"claude": "b"}, nil, at) {
		t.Fatal("change not due")
	}
	w.ran(at)
	if w.due(map[string]string{"claude": "c"}, nil, at.Add(20*time.Second)) {
		t.Fatal("a second change started a refresh within a minute")
	}
	if !w.due(map[string]string{"claude": "c"}, nil, at.Add(loginRefreshGap)) {
		t.Fatal("the second change never started a refresh")
	}
}

func TestLoginWatchCatchesARenewalBeforeItsFirstCheck(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var w loginWatch
	// The startup refresh read the login at revision a; it was renewed to b
	// before the first check.
	if !w.due(map[string]string{"claude": "b", "openai": "x"}, map[string]string{"claude": "a"}, start) {
		t.Fatal("a renewal after the last refresh and before the first check was missed")
	}
	w.ran(start)
	if w.due(map[string]string{"claude": "b", "openai": "x"}, map[string]string{"claude": "a"}, start.Add(2*time.Minute)) {
		t.Fatal("later checks compared with refresh results")
	}
}

func TestLoginWatchFirstCheckIgnoresSourcesTheRefreshDidNotRecord(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var w loginWatch
	if w.due(map[string]string{"claude": "a", "openai": "x"}, map[string]string{"claude": "a"}, start) {
		t.Fatal("a source missing from the last refresh started a refresh")
	}
}
