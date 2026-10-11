package cli

import (
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
