//go:build tray

package tray

import (
	"log"
	"os"
	"path/filepath"

	"golang.org/x/term"
)

// redirectLogToFile sends the default logger and stderr to a log file under
// the user's cache dir when stderr is not a terminal. This covers two cases
// where errors would otherwise be silently lost:
//
//   - Windows tray runs after detaching from the console
//   - Any platform when launched at login (LaunchAgent / .desktop / .lnk
//     in the Startup folder — none of these have a terminal)
//
// When stderr IS a terminal (developer running `clawmeter tray` in a shell),
// we leave it alone so errors still surface inline.
func redirectLogToFile() {
	if term.IsTerminal(int(os.Stderr.Fd())) {
		return
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	dir := filepath.Join(cacheDir, "clawmeter")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "tray.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
	os.Stderr = f
}
