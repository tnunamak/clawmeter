//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package history

import "os"

func tryFileLock(*os.File) (bool, error) { return false, os.ErrInvalid }
func unlockFile(*os.File) error          { return os.ErrInvalid }
