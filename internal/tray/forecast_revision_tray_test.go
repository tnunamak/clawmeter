//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestRevisionNotificationPreservesThresholdSide(t *testing.T) {
	oldCfg, oldSend, oldHistory := cfg, sendThresholdNotification, s.thresholdUtilization
	t.Cleanup(func() { cfg, sendThresholdNotification, s.thresholdUtilization = oldCfg, oldSend, oldHistory })
	cfg = config.DefaultConfig()
	for _, tc := range []struct {
		used float64
		want string
	}{{99.6, "99.6%"}, {99.9999, "<100%"}, {100.0001, ">100%"}} {
		s.thresholdUtilization = nil
		message := ""
		sendThresholdNotification = func(_, body, _ string) { message = body }
		now := time.Now()
		checkThresholds(map[string]*provider.UsageData{"claude": {Provider: "claude", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Utilization: tc.used, ResetsAt: now.Add(3 * time.Hour)}}}}, nil)
		if !strings.Contains(message, "window at "+tc.want) {
			t.Errorf("notification loses current threshold side: %s", message)
		}
	}
}
