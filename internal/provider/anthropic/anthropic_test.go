package anthropic

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestAddUsageWindowsSkipsWindowsWithoutResetTime(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		FiveHour:       &usageWindow{Utilization: 12, ResetsAt: reset},
		SevenDaySonnet: &usageWindow{Utilization: 0},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("len(data.Windows) = %d, want 1", len(data.Windows))
	}
	if got := data.Windows[0].Name; got != "5h" {
		t.Fatalf("window name = %q, want 5h", got)
	}
	if data.Windows[0].ResetsAt.IsZero() {
		t.Fatal("5h reset time should be preserved")
	}
}

func TestAddUsageWindowsIncludesNormalizedScopedModelLimits(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 7, 1, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		Limits: []usageLimit{
			{
				Kind:     "weekly_scoped",
				Percent:  42,
				ResetsAt: reset,
				Scope: &usageLimitScope{
					Model: &usageLimitModelScope{DisplayName: "Fable"},
				},
			},
		},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("len(data.Windows) = %d, want 1", len(data.Windows))
	}
	got := data.Windows[0]
	if got.Name != "7d Fable" || got.DisplayName != "7 days (Fable)" {
		t.Fatalf("window = %q/%q, want 7d Fable/7 days (Fable)", got.Name, got.DisplayName)
	}
	if got.Utilization != 42 || !got.ResetsAt.Equal(reset) {
		t.Fatalf("window usage/reset = %.0f/%s, want 42/%s", got.Utilization, got.ResetsAt, reset)
	}
}

func TestAddUsageWindowsDeduplicatesLegacyAndNormalizedLimits(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 7, 1, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		FiveHour: &usageWindow{Utilization: 12, ResetsAt: reset},
		SevenDay: &usageWindow{Utilization: 34, ResetsAt: reset.Add(24 * time.Hour)},
		Limits: []usageLimit{
			{Kind: "session", Percent: 56, ResetsAt: reset},
			{Kind: "weekly_all", Percent: 78, ResetsAt: reset.Add(24 * time.Hour)},
		},
	})

	if len(data.Windows) != 2 {
		t.Fatalf("len(data.Windows) = %d, want legacy windows only", len(data.Windows))
	}
	if data.Windows[0].Name != "5h" || data.Windows[0].Utilization != 12 {
		t.Fatalf("first window = %+v, want legacy 5h", data.Windows[0])
	}
	if data.Windows[1].Name != "7d All" || data.Windows[1].Utilization != 34 {
		t.Fatalf("second window = %+v, want legacy 7d All", data.Windows[1])
	}
}

func TestUsageUnavailableWhenMainWindowsAreZeroAndModelResetMissing(t *testing.T) {
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	resp := usageResponse{
		FiveHour:       &usageWindow{Utilization: 0, ResetsAt: reset},
		SevenDay:       &usageWindow{Utilization: 0, ResetsAt: reset.Add(7 * time.Hour)},
		SevenDaySonnet: &usageWindow{Utilization: 0},
	}

	if !resp.usageUnavailable() {
		t.Fatal("usageUnavailable() = false, want true")
	}
}

func TestUsageUnavailableAllowsRealZeroWhenModelWindowsAreAbsent(t *testing.T) {
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	resp := usageResponse{
		FiveHour: &usageWindow{Utilization: 0, ResetsAt: reset},
		SevenDay: &usageWindow{Utilization: 0, ResetsAt: reset.Add(7 * time.Hour)},
	}

	if resp.usageUnavailable() {
		t.Fatal("usageUnavailable() = true, want false")
	}
}
