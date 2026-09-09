//go:build windows

package systray

// Windows updates the native menu directly and has no D-Bus layout signal to
// batch. Keep the API portable while leaving its existing behavior unchanged.
func BeginMenuUpdate() {}
func EndMenuUpdate()   {}
