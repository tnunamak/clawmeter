//go:build tray

package tray

import (
	"testing"
	"time"
)

func TestP2ExpiredCallbackKeepsReplacementTimer(t *testing.T) {
	actions := make(chan iconClickAction, 4)
	d := newTrayClickDispatcher(actions, time.Hour)
	d.tapped()
	d.mu.Lock()
	expired := d.timer
	// Expire the timer while holding d.mu, so its callback starts and then
	// waits for the lock. Stop reports false once the callback has started.
	deadline := time.Now().Add(time.Second)
	for expired.Stop() {
		if time.Now().After(deadline) {
			d.mu.Unlock()
			t.Fatal("fixture timer did not expire")
		}
		expired.Reset(time.Millisecond)
		time.Sleep(20 * time.Millisecond)
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
