package anthropic

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestUsageResponseDoesNotTurnMissingUtilizationIntoZero(t *testing.T) {
	var response usageResponse
	if err := json.Unmarshal([]byte(`{"five_hour":{"resets_at":"2026-08-01T00:00:00Z"}}`), &response); err != nil {
		t.Fatal(err)
	}
	data := &provider.UsageData{Provider: "claude"}
	addUsageWindows(data, response)
	if len(data.Windows) != 0 {
		t.Fatalf("windows = %#v, want missing utilization omitted", data.Windows)
	}
}

func TestUsageResponsePreservesExplicitZero(t *testing.T) {
	var response usageResponse
	if err := json.Unmarshal([]byte(`{"five_hour":{"utilization":0,"resets_at":"2026-08-01T00:00:00Z"}}`), &response); err != nil {
		t.Fatal(err)
	}
	data := &provider.UsageData{Provider: "claude"}
	addUsageWindows(data, response)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 {
		t.Fatalf("windows = %#v, want explicit zero usage", data.Windows)
	}
}

func TestExtraUsageRequiresExplicitUtilizationOrAmounts(t *testing.T) {
	var response usageResponse
	if err := json.Unmarshal([]byte(`{"extra_usage":{"is_enabled":true,"monthly_limit":100}}`), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response.ExtraUsage.utilization(); ok {
		t.Fatal("missing used amount was converted into zero utilization")
	}
	if err := json.Unmarshal([]byte(`{"extra_usage":{"is_enabled":true,"monthly_limit":100,"used_credits":0}}`), &response); err != nil {
		t.Fatal(err)
	}
	if utilization, ok := response.ExtraUsage.utilization(); !ok || utilization != 0 {
		t.Fatalf("explicit zero utilization = %v, %t; want 0, true", utilization, ok)
	}
}

func TestAddUsageWindowsSkipsWindowsWithoutResetTime(t *testing.T) {
	data := &provider.UsageData{Provider: "claude"}
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	addUsageWindows(data, usageResponse{
		FiveHour:       &usageWindow{Utilization: float64Ptr(12), ResetsAt: reset},
		SevenDaySonnet: &usageWindow{Utilization: float64Ptr(0)},
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
				Percent:  float64Ptr(42),
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
		FiveHour: &usageWindow{Utilization: float64Ptr(12), ResetsAt: reset},
		SevenDay: &usageWindow{Utilization: float64Ptr(34), ResetsAt: reset.Add(24 * time.Hour)},
		Limits: []usageLimit{
			{Kind: "session", Percent: float64Ptr(56), ResetsAt: reset},
			{Kind: "weekly_all", Percent: float64Ptr(78), ResetsAt: reset.Add(24 * time.Hour)},
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
		FiveHour:       &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset},
		SevenDay:       &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset.Add(7 * time.Hour)},
		SevenDaySonnet: &usageWindow{Utilization: float64Ptr(0)},
	}

	if !resp.usageUnavailable() {
		t.Fatal("usageUnavailable() = false, want true")
	}
}

func TestUsageUnavailableAllowsRealZeroWhenModelWindowsAreAbsent(t *testing.T) {
	reset := time.Date(2026, 5, 28, 21, 40, 0, 0, time.UTC)

	resp := usageResponse{
		FiveHour: &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset},
		SevenDay: &usageWindow{Utilization: float64Ptr(0), ResetsAt: reset.Add(7 * time.Hour)},
	}

	if resp.usageUnavailable() {
		t.Fatal("usageUnavailable() = true, want false")
	}
}

func float64Ptr(value float64) *float64 { return &value }
