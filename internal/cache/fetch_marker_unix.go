//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package cache

import (
	"errors"
	"os"
	"syscall"
)

// tryFileLock takes an exclusive, non-blocking flock on f. It reports false, with
// no error, when another open file holds the lock.
func tryFileLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
