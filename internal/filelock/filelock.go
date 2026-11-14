// Package filelock takes an exclusive lock on a file that several processes
// share. The lock belongs to the open file, so the kernel frees it when the
// holder exits, including when the holder crashed.
package filelock

import (
	"errors"
	"os"
	"time"
)

// ErrUnsupported is returned on platforms without file locks.
var ErrUnsupported = errors.New("file locks are not supported on this platform")

// ErrBusy is returned by Acquire when another holder kept the lock for the whole
// wait.
var ErrBusy = errors.New("file lock is held by another process")

// pollInterval is the pause between attempts to take a busy lock.
const pollInterval = time.Millisecond

// Acquire opens path, creating it if needed, and takes its exclusive lock,
// retrying for up to wait. The returned release function unlocks the file and
// closes it. It returns ErrBusy if the lock stayed held for the whole wait.
//
// The file is never removed. Unlinking a lock file would let a second process
// create and lock a new file at the same path while the first still holds the
// old one, so two holders would each believe they had the lock.
func Acquire(path string, wait time.Duration) (release func(), err error) {
	return AcquireWatched(path, wait, nil)
}

// AcquireWatched is Acquire, and calls onBusy after each attempt that finds the lock
// held, before it pauses or gives up. A caller uses it to learn that it is waiting on
// the lock, not merely that it started. A nil onBusy is not called.
func AcquireWatched(path string, wait time.Duration, onBusy func()) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		locked, err := TryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if locked {
			return func() {
				_ = Unlock(f)
				_ = f.Close()
			}, nil
		}
		if onBusy != nil {
			onBusy()
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, ErrBusy
		}
		time.Sleep(pollInterval)
	}
}
