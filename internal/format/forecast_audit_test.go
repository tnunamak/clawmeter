package format

import (
	"testing"
	"time"
)

func TestAuditN2PositiveSubminuteDuration(t *testing.T) {
	for _, d := range []time.Duration{time.Nanosecond, time.Second, 14 * time.Second, 29 * time.Second, 30 * time.Second, 59 * time.Second} {
		if got := FormatDuration(d); got != "<1m" {
			t.Errorf("duration %v=%q want <1m", d, got)
		}
	}
	if got := FormatDuration(0); got != "0m" {
		t.Errorf("zero=%q", got)
	}
	if got := FormatDuration(-time.Second); got != "now" {
		t.Errorf("negative=%q", got)
	}
}

func TestAuditN1PercentPreservesThresholdSide(t *testing.T) {
	for _, tc := range []struct {
		pct  float64
		want string
	}{
		{12.5, "13%"}, {89.6, "89.6%"}, {90, "90%"}, {90.4, "90.4%"},
		{99.6, "99.6%"}, {100, "100%"}, {100.4, "100.4%"},
		{89.9999, "<90%"}, {90.0001, ">90%"}, {99.9999, "<100%"}, {100.0001, ">100%"},
	} {
		if got := Percent(tc.pct); got != tc.want {
			t.Errorf("Percent(%v)=%q want %q", tc.pct, got, tc.want)
		}
	}
}
