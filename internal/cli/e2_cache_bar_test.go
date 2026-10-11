package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
)

func TestBarHandlesInvalidUtilization(t *testing.T) {
	for _, pct := range []float64{-100, -5, math.NaN(), math.Inf(1), math.Inf(-1), 0, 100, 150} {
		t.Run(fmt.Sprint(pct), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("bar(%v) panicked: %v", pct, r)
				}
			}()
			got := bar(pct)
			if utf8.RuneCountInString(got) != barWidth {
				t.Errorf("bar(%v) width = %d, want %d", pct, utf8.RuneCountInString(got), barWidth)
			}
			if (math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 0) && got != strings.Repeat("░", barWidth) {
				t.Errorf("bar(%v) = %q, want empty bar", pct, got)
			}
		})
	}
}
func TestCachedStatusMarksOldReadingsStale(t *testing.T) {
	for _, age := range []time.Duration{10 * time.Second, 75 * time.Second, 72 * time.Hour} {
		registry := provider.NewRegistry()
		registry.Register(cliStubProvider{name: "claude"})
		data := &provider.UsageData{Provider: "claude", FetchedAt: time.Now().Add(-age), Windows: []provider.UsageWindow{{Name: "5h", Utilization: 30, ResetsAt: time.Now().Add(time.Hour)}}}
		entry := &cache.Entry{FetchedAt: time.Now().Add(-age), ProviderData: map[string]*provider.UsageData{"claude": data}}
		output := buildOutputFromCache(registry, config.DefaultConfig(), entry)
		stale := age > 2*time.Minute
		if len(output.Providers) != 1 {
			t.Fatalf("providers: %#v", output.Providers)
		}
		if output.Providers[0].Data.Stale != stale {
			t.Errorf("cache age %s: stale = %v, want %v", age, output.Providers[0].Data.Stale, stale)
		}
		for name, line := range map[string]string{"statusline": output.StatusLineSummary(), "plain": output.Providers[0].FormatPlain(), "agent": output.AgentSummary()} {
			if strings.Contains(line, "stale") != stale {
				t.Errorf("cache age %s: %s = %q, want stale=%v", age, name, line, stale)
			}
		}
		if stale && !strings.Contains(output.AgentSummary(), "status=stale") {
			t.Errorf("old agent summary still reports current status: %s", output.AgentSummary())
		}
		if data.Stale {
			t.Error("cache output mutated source entry")
		}
	}
}

func TestStatusLineReadsDaysOldCacheFromDisk(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("APPDATA", root)
	t.Setenv("SYNTHETIC_API_KEY", "test-cache-statusline")
	cfg, err := config.Load(all.SourceValidator())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers["synthetic"] = config.ProviderConfig{Enabled: true}
	if err := cfg.Save(all.SourceValidator()); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	all.Register(registry, cfg)
	p, ok := registry.Get("synthetic")
	if !ok || !p.IsConfigured() {
		t.Fatal("fixture provider is not configured")
	}
	for _, age := range []time.Duration{10 * time.Second, 72 * time.Hour} {
		observed := time.Now().Add(-age)
		data := &provider.UsageData{Provider: p.Name(), SourceID: provider.SourceID(p), FetchedAt: observed, Windows: []provider.UsageWindow{{Name: "5h", Utilization: 30, ResetsAt: time.Now().Add(time.Hour)}}}
		if err := cache.Write(&provider.MultiFetchResult{Results: map[string]*provider.UsageData{provider.SourceKey(p): data}, SourceRevisions: sourceRevisions([]provider.Provider{p}), FetchedAt: observed}); err != nil {
			t.Fatal(err)
		}
		output, code := loadCachedStatusOutput(false)
		if code != 0 {
			t.Fatalf("cached status exit = %d", code)
		}
		line := output.StatusLineSummary()
		if !strings.Contains(line, "Synthetic") || strings.Contains(line, "stale") != (age > time.Minute) {
			t.Errorf("cache age %s: statusline = %q", age, line)
		}
		disk, err := cache.Read()
		if err != nil {
			t.Fatal(err)
		}
		if disk.ProviderData[provider.SourceKey(p)].Stale {
			t.Error("cached-only output rewrote persisted usage")
		}
	}
}

type e2RedteamRevisionedBlockingProvider struct {
	cliStubProvider
	started, release, finished chan struct{}
}

func (p e2RedteamRevisionedBlockingProvider) SourceRevision() string {
	return "test-only-unchanged-source-revision"
}

func (p e2RedteamRevisionedBlockingProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	close(p.started)
	<-p.release
	defer close(p.finished)
	return &provider.UsageData{Provider: p.Name(), FetchedAt: time.Now()}, nil
}

func TestE2RedteamCancellationKeepsMatchingCacheFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LOCALAPPDATA", home)
	p := e2RedteamRevisionedBlockingProvider{cliStubProvider: cliStubProvider{name: "e2-redteam"}}
	p.started, p.release, p.finished = make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan *provider.MultiFetchResult, 1)
	go func() { returned <- provider.FetchProvidersParallel(ctx, []provider.Provider{p}) }()
	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		close(p.release)
		t.Fatal("test provider did not start")
	}
	defer func() {
		close(p.release)
		<-p.finished
	}()
	cancel()
	var result *provider.MultiFetchResult
	select {
	case result = <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not return after cancellation")
	}
	key := provider.SourceKey(p)
	if !errors.Is(result.Errors[key], context.Canceled) {
		t.Fatalf("fetch error = %v, want context.Canceled", result.Errors[key])
	}
	old := &provider.UsageData{
		Provider: p.Name(), SourceID: provider.SourceID(p), FetchedAt: time.Now().Add(-time.Minute),
		Windows: []provider.UsageWindow{{Name: "5h", Utilization: 25, ResetsAt: time.Now().Add(time.Hour)}},
	}
	entry := &cache.Entry{ProviderData: map[string]*provider.UsageData{key: old}, SourceRevisions: map[string]string{key: p.SourceRevision()}}
	// This is the fallback used by both loadStatusOutput and Check. The source
	// never changed, so cancellation must not discard its known-good reading.
	cached, ok := staleFallback(entry, key, result.Results[key], result.SourceRevisions[key])
	if !ok || cached == nil || !cached.Stale || len(cached.Windows) != 1 {
		t.Fatalf("cancellation discarded matching cached usage: revision=%q, cached=%#v, ok=%v",
			result.SourceRevisions[key], cached, ok)
	}
}

func TestE2RedteamFreshLocalUpdateInOldCacheIsNotStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LOCALAPPDATA", home)
	now := time.Now()
	old := now.Add(-time.Hour)
	reading := func(name string, fetched time.Time) *provider.UsageData {
		return &provider.UsageData{
			Provider: name, FetchedAt: fetched,
			Windows: []provider.UsageWindow{{Name: "5h", Utilization: 25, ResetsAt: now.Add(time.Hour)}},
		}
	}
	if err := cache.Write(&provider.MultiFetchResult{
		Results:   map[string]*provider.UsageData{"claude": reading("claude", old), "openai": reading("openai", old)},
		FetchedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpdateProvider("claude", reading("claude", now), ""); err != nil {
		t.Fatal(err)
	}
	entry, err := cache.Read()
	if err != nil {
		t.Fatal(err)
	}
	// UpdateProvider intentionally preserves the aggregate timestamp so that
	// updating one source does not refresh unrelated sources.
	if !entry.FetchedAt.Equal(old) {
		t.Fatalf("fixture changed aggregate timestamp: %v", entry.FetchedAt)
	}
	registry := provider.NewRegistry()
	for _, name := range []string{"claude", "openai"} {
		if err := registry.Register(cliStubProvider{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	output := buildOutputFromCache(registry, config.DefaultConfig(), entry)
	if len(output.Providers) != 2 {
		t.Fatalf("providers = %#v", output.Providers)
	}
	for _, row := range output.Providers {
		wantStale := row.Name == "openai"
		if row.Data == nil || row.Data.Stale != wantStale {
			t.Errorf("%s data = %#v, want stale=%v based on the source reading", row.Name, row.Data, wantStale)
		}
	}
}
