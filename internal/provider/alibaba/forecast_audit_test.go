package alibaba

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
)

func TestAuditM2UnknownMonthlyStart(t *testing.T) {
	p := New(config.ProviderConfig{})
	p.now = func() time.Time { return testNow }
	data := p.transformQuota(map[string]any{"perBillMonthUsedQuota": 9.0, "perBillMonthTotalQuota": 100.0, "perBillMonthQuotaNextRefreshTime": "2026-03-01T00:00:00Z"})
	if len(data.Windows) != 1 {
		t.Fatalf("windows=%+v", data.Windows)
	}
	if got := forecast.WindowLength(data.Windows[0]); got != 0 {
		t.Fatalf("unknown billing start invented duration %v", got)
	}
}
