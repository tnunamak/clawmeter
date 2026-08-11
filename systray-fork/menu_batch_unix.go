//go:build (linux || freebsd || openbsd || netbsd) && !android

package systray

// BeginMenuUpdate coalesces native menu layout signals until EndMenuUpdate.
// Linux desktop shells can synchronously rebuild a tray menu for each
// LayoutUpdated signal; batching keeps a provider refresh from monopolizing
// the shell while the menu state is being updated.
func BeginMenuUpdate() {
	instance.lock.Lock()
	instance.menuUpdateDepth++
	instance.lock.Unlock()
}

// EndMenuUpdate publishes one layout update for all mutations in the batch.
func EndMenuUpdate() {
	instance.lock.Lock()
	defer instance.lock.Unlock()
	if instance.menuUpdateDepth == 0 {
		return
	}
	instance.menuUpdateDepth--
	if instance.menuUpdateDepth == 0 && instance.menuUpdateDirty {
		instance.menuUpdateDirty = false
		refreshLocked()
	}
}
