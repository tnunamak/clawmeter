//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package resetfeed

import "os"

// tryLock on platforms without a supported lock primitive always succeeds, so
// reads run unserialized there. Clawmeter ships for linux, darwin and windows.
func tryLock(*os.File) (bool, error) {
	return true, nil
}
