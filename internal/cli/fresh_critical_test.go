package cli

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// Only a fresh reading may be critical. A retained reading's full window is a
// warning, and so is one with no observation time, because the reading cannot
// be fresh. The JSON order and the summary both read this tier.
func TestOnlyFreshFullWindowIsCritical(t *testing.T) {
	now := time.Now()
	full := func(retained bool) ProviderFormatter {
		data := &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{{
			Name: "5h", Utilization: 100, ResetsAt: now.Add(30 * time.Minute),
		}}}
		if retained {
			data.MarkStale("cache expired")
		}
		return ProviderFormatter{Name: "test", Data: data}
	}

	fresh := full(false)
	if got := summaryTierOf(&fresh); got != 2 {
		t.Errorf("fresh full window: tier = %d, want 2 (critical)", got)
	}
	retained := full(true)
	if got := summaryTierOf(&retained); got != 3 {
		t.Errorf("retained full window: tier = %d, want 3 (warning)", got)
	}

	// A window past its reset is a warning. A fresh full window beside it
	// is still critical.
	mixed := ProviderFormatter{Name: "test", Data: &provider.UsageData{FetchedAt: now, Windows: []provider.UsageWindow{
		{Name: "5h", Utilization: 100, ResetsAt: now.Add(30 * time.Minute)},
		{Name: "7d", Utilization: 10, ResetsAt: now.Add(-time.Minute)},
	}}}
	if got := summaryTierOf(&mixed); got != 2 {
		t.Errorf("fresh full window beside a passed reset: tier = %d, want 2 (critical)", got)
	}

	unstamped := ProviderFormatter{Name: "test", Data: &provider.UsageData{Windows: []provider.UsageWindow{
		{Name: "5h", Utilization: 100, ResetsAt: now.Add(30 * time.Minute)},
	}}}
	if got := summaryTierOf(&unstamped); got != 3 {
		t.Errorf("unstamped full window: tier = %d, want 3 (warning)", got)
	}
}
