package alibabatoken

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
)

func TestAuditM2UnknownMonthlyStart(t *testing.T) {
	data, err := parseUsage(map[string]any{"per1MonthPercentage": 0.09, "per1MonthResetTime": "2026-03-01T00:00:00Z"}, time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := forecast.WindowLength(data.Windows[0]); got != 0 {
		t.Fatalf("unknown billing start invented duration %v", got)
	}
}
