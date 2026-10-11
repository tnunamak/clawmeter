//go:build tray

package tray

import (
	"testing"
	"time"
)

func TestP2ExpiredCallbackKeepsReplacementTimer(t *testing.T) {
	actions := make(chan iconClickAction, 4)
	d := newTrayClickDispatcher(actions, 10*time.Millisecond)
	d.tapped()
	d.mu.Lock()
	time.Sleep(30 * time.Millisecond)
	if d.timer.Stop() {
		d.mu.Unlock()
		t.Fatal("fixture timer did not expire")
	}
	replacement := time.NewTimer(time.Hour)
	defer replacement.Stop()
	d.timer = replacement
	d.mu.Unlock()
	select {
	case <-actions:
	case <-time.After(time.Second):
		t.Fatal("expired callback never completed")
	}
	d.mu.Lock()
	got := d.timer
	d.mu.Unlock()
	if got != replacement {
		t.Fatal("expired callback cleared replacement timer")
	}
}
