//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package config

import (
	"errors"
	"os"
)

func tryLock(*os.File) (bool, error) {
	return false, errors.New("config file locking is unsupported on this platform")
}
