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
