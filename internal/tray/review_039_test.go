//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestReview039TrayUsesTypedMonthlyLength(t *testing.T) {
	w := provider.UsageWindow{Name: "premium", Length: 30 * 24 * time.Hour,
		Utilization: 60, ResetsAt: time.Now().Add(12 * time.Hour)}
	got := viewWindowOf(t, w)
	if got.Forecast.State != view.ForecastKnown || got.Forecast.Pct < 60 || got.Forecast.Pct > 62 {
		t.Fatalf("tray forecast=%+v; menu=%q", got.Forecast, trayWindowText(w))
	}
}

func TestReview039TrayUnknownIsNotSafe(t *testing.T) {
	w := provider.UsageWindow{Name: "5h", Utilization: 100,
		ResetsAt: time.Now().Add(299 * time.Minute)}
	snap := viewOf(map[string]*provider.UsageData{"claude": {Windows: []provider.UsageWindow{w}}}, time.Now())
	got := Tooltip(snap, nil)
	_, meter := IconState(snap)
	if strings.Contains(got, "Won't run out") || strings.Contains(got, "Est. 0%") || meter.RiskPct < 100 {
		t.Fatalf("exhausted quota presented as safe: tooltip=%q meter=%+v", got, meter)
	}
}

func TestReview039ExpiredResetDoesNotRenotify(t *testing.T) {
	oldCfg, oldSend, oldHistory := cfg, sendThresholdNotification, s.thresholdUtilization
	cfg = config.DefaultConfig()
	s.thresholdUtilization = nil
	t.Cleanup(func() { cfg, sendThresholdNotification, s.thresholdUtilization = oldCfg, oldSend, oldHistory })
	count := 0
	sendThresholdNotification = func(string, string, string) { count++ }
	data := map[string]*provider.UsageData{"claude": {Windows: []provider.UsageWindow{{
		Name: "5h", Utilization: 96, ResetsAt: time.Now().Add(-time.Minute),
	}}}}
	checkThresholds(data, nil)
	first := count
	checkThresholds(data, nil)
	if count != first {
		t.Fatalf("same expired window notified again: first=%d after second render=%d", first, count)
	}
}
