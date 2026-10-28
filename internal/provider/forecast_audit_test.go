package provider

import (
	"testing"
	"time"
)

func TestAuditM2MonthlyNameDoesNotEstablishDuration(t *testing.T) {
	if got := WindowLengthFromName("monthly"); got != 0 {
		t.Errorf("monthly name invented duration %v", got)
	}
	if got := WindowDuration(1, "month"); got != 0 {
		t.Errorf("month unit invented duration %v", got)
	}
	if got := WindowDuration(30, "day"); got != 30*24*time.Hour {
		t.Errorf("explicit 30-day duration=%v", got)
	}
}
