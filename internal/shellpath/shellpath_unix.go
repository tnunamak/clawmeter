//go:build !windows

package shellpath

import (
	"os"

	"golang.org/x/term"
)

func pathEntryKey(path string) string {
	return path
}

func capture() []string {
	return captureLoginShell()
}

func terminalAvailable() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
