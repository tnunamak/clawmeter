//go:build tray && !windows

package tray

import "time"

func trayDoubleClickWindow() time.Duration { return defaultDoubleClickWindow }
