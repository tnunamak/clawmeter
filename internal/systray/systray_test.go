package systray

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestClickIsBufferedWhenNobodyIsReceiving(t *testing.T) {
	item := newMenuItem("x", "", nil)
	menuItemsLock.Lock()
	menuItems[item.id] = item
	menuItemsLock.Unlock()

	systrayMenuItemSelected(item.id)
	systrayMenuItemSelected(item.id) // second click is dropped, must not block
	select {
	case <-item.ClickedCh:
	default:
		t.Fatal("click was dropped although the channel had room")
	}
}

func TestClickRacingRemoveDoesNotPanic(t *testing.T) {
	for i := 0; i < 200; i++ {
		item := newMenuItem("x", "", nil)
		menuItemsLock.Lock()
		menuItems[item.id] = item
		menuItemsLock.Unlock()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); systrayMenuItemSelected(item.id) }()
		go func() { defer wg.Done(); item.Remove() }()
		wg.Wait()
	}
}

func TestRunSystrayExitRunsOnce(t *testing.T) {
	var calls atomic.Int32
	systrayExit = func() { calls.Add(1) }
	systrayExitCalled.Store(false)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); runSystrayExit() }()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("exit callback ran %d times, want 1", n)
	}
}
