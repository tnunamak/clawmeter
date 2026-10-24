package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const configLockTimeout = 5 * time.Second

// Separate the nonblocking attempt so tests can observe actual contention,
// rather than relying on sleeps or scheduler timing.
var tryConfigLock = tryLock

// Update serializes a read/modify/write transaction across processes. The
// callback must not call Update (or SetProviderEnabled) recursively. Returning
// an error aborts the save. Validators run on both the loaded and saved config.
func Update(fn func(*Config) error, validators ...SourceValidator) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	lock, err := acquireConfigLock(path, configLockTimeout)
	if err != nil {
		return err
	}
	// Closing releases the OS lock, including when the callback panics. Never
	// unlink the sidecar: rename replaces config.yaml, but the lock inode must
	// remain stable for processes that already have it open.
	defer lock.Close()

	cfg, err := Load(validators...)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return cfg.Save(validators...)
}

func acquireConfigLock(path string, timeout time.Duration) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		locked, err := tryConfigLock(lock)
		if err != nil {
			lock.Close()
			return nil, fmt.Errorf("lock config: %w", err)
		}
		if locked {
			return lock, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			lock.Close()
			return nil, fmt.Errorf("timed out waiting for config lock after %s", timeout)
		}
		if remaining > 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}
