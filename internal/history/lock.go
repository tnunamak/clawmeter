package history

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var processLock sync.Mutex
var tryLock = tryFileLock

func acquireLock(dir string) (interface{ Close() error }, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create history directory: %w", err)
	}
	processLock.Lock()
	path := filepath.Join(dir, ".usage-history.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		processLock.Unlock()
		return nil, fmt.Errorf("open history lock: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		processLock.Unlock()
		return nil, fmt.Errorf("protect history lock: %w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		locked, err := tryLock(file)
		if err != nil {
			file.Close()
			processLock.Unlock()
			return nil, fmt.Errorf("lock history: %w", err)
		}
		if locked {
			return &lockedFile{File: file}, nil
		}
		if time.Until(deadline) <= 0 {
			file.Close()
			processLock.Unlock()
			return nil, fmt.Errorf("timed out waiting for history lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type lockedFile struct{ *os.File }

func (f *lockedFile) Close() error {
	err := unlockFile(f.File)
	if closeErr := f.File.Close(); err == nil {
		err = closeErr
	}
	processLock.Unlock()
	return err
}
