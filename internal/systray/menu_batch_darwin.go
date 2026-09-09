//go:build darwin && !ios

package systray

// The native Cocoa menu does not use D-Bus layout signals. Keep the API
// portable while leaving Darwin's existing mutation behavior unchanged.
func BeginMenuUpdate() {}
func EndMenuUpdate()   {}
