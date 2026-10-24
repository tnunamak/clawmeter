//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows

package config

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpdateConcurrentChanges(t *testing.T) {
	scopeHome(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	blocked := make(chan struct{})
	var releaseOnce, blockedOnce sync.Once
	var workers sync.WaitGroup
	original := tryConfigLock
	tryConfigLock = func(f *os.File) (bool, error) {
		locked, err := original(f)
		if !locked && err == nil {
			blockedOnce.Do(func() { close(blocked) })
		}
		return locked, err
	}
	defer func() {
		releaseOnce.Do(func() { close(release) })
		workers.Wait()
		tryConfigLock = original
	}()

	first := make(chan error, 1)
	second := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		first <- Update(func(cfg *Config) error {
			cfg.EnsureProvider("first", true)
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("first update did not enter callback: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first update did not enter callback")
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		second <- Update(func(cfg *Config) error {
			cfg.EnsureProvider("second", true)
			return nil
		})
	}()
	select {
	case <-blocked:
		// Only release the first callback once the OS rejected the second
		// writer's lock attempt. No timing assumption establishes ordering.
	case err := <-second:
		t.Fatalf("second update did not block: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("second update did not attempt the lock")
	}
	releaseOnce.Do(func() { close(release) })
	for _, done := range []<-chan error{first, second} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProviderExplicitlyEnabled("first") || !cfg.IsProviderExplicitlyEnabled("second") {
		t.Fatal("concurrent update lost a provider change")
	}
}

func TestUpdateCallbackErrorPreservesFile(t *testing.T) {
	scopeHome(t)
	if err := DefaultConfig().Save(); err != nil {
		t.Fatal(err)
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("abort transaction")
	err = Update(func(cfg *Config) error {
		cfg.EnsureProvider("not-saved", true)
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("Update error = %v, want %v", err, want)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("aborted update changed file: %v", err)
	}
	// Aborting must release the lock for the next transaction.
	if err := Update(func(cfg *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMissingConfig(t *testing.T) {
	scopeHome(t)
	if err := Update(func(cfg *Config) error {
		if cfg.Settings.PollInterval != MinimumPollIntervalSeconds || cfg.Providers == nil {
			return errors.New("missing config did not load defaults")
		}
		cfg.EnsureProvider("new", true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || !cfg.IsProviderExplicitlyEnabled("new") {
		t.Fatalf("missing config update was not saved: %v", err)
	}
}

func TestConfigLockTimeoutAndStableSidecar(t *testing.T) {
	scopeHome(t)
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireConfigLock(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	before, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireConfigLock(path, time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out waiting for config lock") {
		t.Fatalf("contention error = %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Update(func(cfg *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path + ".lock")
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("transaction replaced or removed sidecar: %v", err)
	}
}

func TestUpdateSaveValidationDoesNotWrite(t *testing.T) {
	scopeHome(t)
	want := errors.New("provider validation rejected")
	err := Update(func(cfg *Config) error {
		cfg.Providers["example"] = ProviderConfig{Sources: []SourceConfig{{ID: "default", Credential: CredentialRef{Kind: "native"}}}}
		return nil
	}, func(string, []SourceConfig) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("Update validation error = %v, want %v", err, want)
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected update wrote config: %v", err)
	}
}
