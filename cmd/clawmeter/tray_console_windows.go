//go:build windows

package main

import "syscall"

var freeConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole")

func prepareTrayConsole() {
	// Windows release builds are console-subsystem binaries so CLI output works
	// correctly in PowerShell pipes. When the same binary runs the tray, detach
	// from any console so Start Menu/startup launches do not leave a terminal
	// window behind. Errors still go to the tray log when stderr is unavailable.
	_, _, _ = freeConsole.Call()
}
