//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package config

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock without blocking. It reports false, not
// an error, when another process holds the lock.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}
