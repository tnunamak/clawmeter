//go:build tray

package tray

import (
	"sync"
	"time"
)

const trayDoubleClickWindow = 240 * time.Millisecond

type iconClickAction int

const (
	iconClickCycle iconClickAction = iota
	iconClickResetAuto
)

type trayClickDispatcher struct {
	mu     sync.Mutex
	timer  *time.Timer
	window time.Duration
	ch     chan<- iconClickAction
}

func newTrayClickDispatcher(ch chan<- iconClickAction, window time.Duration) *trayClickDispatcher {
	return &trayClickDispatcher{
		window: window,
		ch:     ch,
	}
}

func (d *trayClickDispatcher) tapped() {
	d.mu.Lock()
	if d.timer != nil {
		if d.timer.Stop() {
			d.timer = nil
			d.mu.Unlock()
			sendIconClickAction(d.ch, iconClickResetAuto)
			return
		}
		d.timer = nil
	}
	d.timer = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		d.timer = nil
		d.mu.Unlock()
		sendIconClickAction(d.ch, iconClickCycle)
	})
	d.mu.Unlock()
}

func sendIconClickAction(ch chan<- iconClickAction, action iconClickAction) {
	select {
	case ch <- action:
	default:
	}
}
