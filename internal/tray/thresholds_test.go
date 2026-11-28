//go:build tray

package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestThresholdNotificationMatchesIconRunOutTime(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	outSince := now.Add(-time.Hour)
	reset := now.Add(2 * time.Hour)
	const source = "fixture:threshold"
	const account = "fixture:account"
	windowExhaustion.mu.Lock()
	oldPath, oldGap := windowExhaustion.path, windowExhaustion.maxGap
	oldIdentities, oldObserved, oldLog := windowExhaustion.identities, windowExhaustion.observed, windowExhaustion.log
	windowExhaustion.path = ""
	windowExhaustion.observed = nil
	windowExhaustion.log = exhaustionLog{}
	windowExhaustion.mu.Unlock()
	defer func() {
		windowExhaustion.mu.Lock()
		windowExhaustion.path, windowExhaustion.maxGap = oldPath, oldGap
		windowExhaustion.identities, windowExhaustion.observed, windowExhaustion.log = oldIdentities, oldObserved, oldLog
		windowExhaustion.mu.Unlock()
	}()
	observeReading(&windowExhaustion, source, account, 70, reset, outSince.Add(-time.Minute))
	observeReading(&windowExhaustion, source, account, 100, reset, outSince)
	data := readingAt(100, reset)
	data.FetchedAt = now
	results := map[string]*provider.UsageData{source: data}

	s.mu.Lock()
	oldSamples, oldConfig := s.thresholdUtilization, cfg
	s.thresholdUtilization, cfg = nil, nil
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.thresholdUtilization, cfg = oldSamples, oldConfig
		s.mu.Unlock()
	}()

	icon := view.Build(trayViewInputs(results, nil, now, time.Time{}, nil), now)
	accountView := accountNamed(icon, source)
	if len(accountView.Windows) != 1 {
		t.Fatalf("fixture did not build one icon window: %+v", accountView.Windows)
	}
	want := exhaustionLine(accountView.Windows[0], now)
	if !strings.Contains(strings.ToLower(want), "out since ") {
		t.Fatalf("fixture did not retain run-out history: %q", want)
	}

	oldSend := sendThresholdNotification
	defer func() { sendThresholdNotification = oldSend }()
	var bodies []string
	sendThresholdNotification = func(_, body, _ string) {
		bodies = append(bodies, body)
	}
	checkThresholds(results, nil)
	if len(bodies) != 1 || !strings.Contains(strings.ToLower(bodies[0]), strings.ToLower(want)) {
		t.Fatalf("notification %q does not include icon exhaustion wording %q", bodies, want)
	}
}
