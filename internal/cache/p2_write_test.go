package cache

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func e2CacheHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
}
func TestCacheWriteIgnoresFixedTemp(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			e2CacheHome(t)
			path, err := cachePath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "target")
			sentinel := []byte("unchanged")
			if err := os.WriteFile(target, sentinel, 0644); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(target, path+".tmp"); err != nil {
					t.Skip(err)
				}
			} else {
				if err := os.WriteFile(path+".tmp", sentinel, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := Write(&provider.MultiFetchResult{Results: map[string]*provider.UsageData{"fake": {Provider: "fake"}}, FetchedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Errorf("cache mode = %o, want 600", info.Mode().Perm())
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(sentinel) {
				t.Errorf("symlink target was overwritten: %q", got)
			}
			got, err = os.ReadFile(path + ".tmp")
			if err != nil || string(got) != string(sentinel) {
				t.Errorf("pre-existing temp was touched: %q, %v", got, err)
			}
		})
	}
}
func TestCacheConcurrentProcesses(t *testing.T) {
	if os.Getenv("CLAWMETER_E2_CACHE_WRITER") == "1" {
		label := os.Getenv("CLAWMETER_E2_CACHE_LABEL")
		for i := 0; i < 30; i++ {
			if err := Write(&provider.MultiFetchResult{Results: map[string]*provider.UsageData{label: {Provider: label, Warning: strings.Repeat(label, 5000)}}, FetchedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	e2CacheHome(t)
	type child struct {
		cmd    *exec.Cmd
		output strings.Builder
	}
	children := make([]*child, 8)
	for i := range children {
		c := &child{cmd: exec.Command(os.Args[0], "-test.run=^TestCacheConcurrentProcesses$", "-test.count=1")}
		c.cmd.Env = append(os.Environ(), "CLAWMETER_E2_CACHE_WRITER=1", fmt.Sprintf("CLAWMETER_E2_CACHE_LABEL=writer%d", i))
		c.cmd.Stdout = &c.output
		c.cmd.Stderr = &c.output
		children[i] = c
		if err := c.cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range children {
		if err := c.cmd.Wait(); err != nil {
			t.Errorf("concurrent writer failed: %v: %s", err, c.output.String())
		}
	}
	entry, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.ProviderData) != 1 {
		t.Fatalf("incomplete cache: %#v", entry)
	}
	for key, data := range entry.ProviderData {
		if data.Provider != key || data.Warning != strings.Repeat(key, 5000) {
			t.Errorf("mixed writer content: %q", key)
		}
	}
	path, _ := cachePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("cache mode = %o", info.Mode().Perm())
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".usage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) > 0 {
		t.Errorf("leftover temps: %v", leftovers)
	}
}
