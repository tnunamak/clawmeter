//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package cache

import "os"

// tryFileLock fails on platforms without file locks, so TakeFetchMarker returns
// an error and its caller reads without a marker.
func tryFileLock(*os.File) (bool, error) { return false, os.ErrInvalid }
func unlockFile(*os.File) error          { return os.ErrInvalid }
