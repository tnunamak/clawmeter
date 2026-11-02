package history

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var processLock = make(chan struct{}, 1)
var tryLock = tryFileLock

func acquireLock(dir string) (interface{ Close() error }, error) {
	return acquireLockWithTimeout(dir, lockTimeout)
}

// One budget covers the process gate and OS-lock retries. Filesystem calls are
// not interruptible here, so this is not a guarantee against a hung filesystem.
func acquireLockWithTimeout(dir string, timeout time.Duration) (interface{ Close() error }, error) {
	deadline := time.Now().Add(timeout)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create history directory: %w", err)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, fmt.Errorf("timed out waiting for history lock")
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case processLock <- struct{}{}:
	case <-timer.C:
		return nil, fmt.Errorf("timed out waiting for history lock")
	}
	// The gate and timer may both be ready. Never start a new retry budget or
	// proceed with an expired one just because select chose the gate.
	if time.Until(deadline) <= 0 {
		<-processLock
		return nil, fmt.Errorf("timed out waiting for history lock")
	}
	path := filepath.Join(dir, ".usage-history.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		<-processLock
		return nil, fmt.Errorf("open history lock: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		<-processLock
		return nil, fmt.Errorf("protect history lock: %w", err)
	}
	for {
		if time.Until(deadline) <= 0 {
			file.Close()
			<-processLock
			return nil, fmt.Errorf("timed out waiting for history lock")
		}
		locked, err := tryLock(file)
		if err != nil {
			file.Close()
			<-processLock
			return nil, fmt.Errorf("lock history: %w", err)
		}
		if locked {
			return &lockedFile{File: file}, nil
		}
		delay := time.Until(deadline)
		if delay > 10*time.Millisecond {
			delay = 10 * time.Millisecond
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
}

type lockedFile struct{ *os.File }

func (f *lockedFile) Close() error {
	err := unlockFile(f.File)
	if closeErr := f.File.Close(); err == nil {
		err = closeErr
	}
	<-processLock
	return err
}
