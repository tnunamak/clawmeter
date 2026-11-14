//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package filelock

import "os"

// TryLock fails on platforms without file locks, so Acquire returns ErrUnsupported.
func TryLock(*os.File) (bool, error) { return false, ErrUnsupported }

func Unlock(*os.File) error { return nil }
