package systray

import (
	"sync"
	"testing"
)

func TestP2RemoveTwice(t *testing.T) {
	item := newMenuItem("x", "", nil)
	menuItemsLock.Lock()
	menuItems[item.id] = item
	menuItemsLock.Unlock()
	item.Remove()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second removal panicked: %v", r)
		}
	}()
	item.Remove()
	item.SetTitle("removed")
	menuItemsLock.RLock()
	_, present := menuItems[item.id]
	menuItemsLock.RUnlock()
	if present {
		t.Fatal("removed item resurrected")
	}
}
func TestP2ResetMenuConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			ResetMenu()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			item := newMenuItem("x", "", nil)
			item.update()
			item.Remove()
		}
	}()
	wg.Wait()
}
