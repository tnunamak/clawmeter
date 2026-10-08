package provider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// utc pins local time so dates render the same everywhere.
func utc(t *testing.T) {
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// The live Console snapshot of 2026-10-08: one $200 plan credit granted that
// morning, 11¢ spent, expiring Oct 25.
func TestCreditPoolProjectsTheLiveConsoleSnapshot(t *testing.T) {
	utc(t)
	observed := time.Date(2026, 10, 8, 17, 0, 0, 0, time.UTC)
	expires := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	pool := NewCreditPool("Family's Individual Organization", "USD", 19988, 11, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		[]CreditGrant{{Remaining: 19988, ExpiresAt: expires}},
		[]DailySpend{{Day: day("2026-10-08"), Amount: 11.2965}}, observed, observed)

	if pool.Balance != 19988 || pool.MonthSpend == nil || *pool.MonthSpend != 11 || !pool.ExpiresAt.Equal(expires) || pool.Expiring != 19988 {
		t.Fatalf("pool = %+v", pool)
	}
	// Under a day of data counts as one day: 11.2965¢/day.
	if pool.BurnDays != 1 || pool.BurnPerDay < 11.29 || pool.BurnPerDay > 11.30 {
		t.Fatalf("burn = %v over %d days", pool.BurnPerDay, pool.BurnDays)
	}
	// 16.29 days left × 11.2965¢ = 184¢.
	if pool.ProjectedSpend != 184 || pool.ProjectedUnused != 19988-184 || !pool.RunsOutAt.IsZero() {
		t.Fatalf("projection = spend %d unused %d runs out %v", pool.ProjectedSpend, pool.ProjectedUnused, pool.RunsOutAt)
	}
	now := observed.Add(time.Hour)
	if got, want := pool.Summary(now), "$199.88 left · spent $0.11 this month · on pace to spend $1.84 more before it expires Oct 25 00:00 · checked "; !strings.HasPrefix(got, want) {
		t.Fatalf("Summary() = %q, want prefix %q", got, want)
	}
	if got, want := pool.TrayLine(now), "$199.88 left · $198.04 expires Oct 25 00:00 · checked "; !strings.HasPrefix(got, want) {
		t.Fatalf("TrayLine() = %q, want prefix %q", got, want)
	}
}

func TestCreditPoolRunsOutBeforeExpiry(t *testing.T) {
	utc(t)
	observed := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	expires := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	daily := []DailySpend{}
	for d := 4; d <= 10; d++ {
		daily = append(daily, DailySpend{Day: time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC), Amount: 1000})
	}
	// Spend from 30 days ago is outside the 7-day window.
	daily = append(daily, DailySpend{Day: day("2026-09-10"), Amount: 99999})
	pool := NewCreditPool("Org", "USD", 5000, 7000, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		[]CreditGrant{{Remaining: 5000, ExpiresAt: expires}}, daily, observed, observed)
	// $70 over 6.5 days.
	if pool.BurnDays != 7 || pool.BurnPerDay < 1076 || pool.BurnPerDay > 1077 {
		t.Fatalf("burn = %v over %d days", pool.BurnPerDay, pool.BurnDays)
	}
	if pool.ProjectedSpend != 5000 || pool.ProjectedUnused != 0 || pool.RunsOutAt.IsZero() || !pool.RunsOutAt.Before(expires) {
		t.Fatalf("projection = %+v", pool)
	}
	if got := pool.TrayLine(observed); !strings.HasPrefix(got, "$50.00 left · runs out Oct 15 · ") {
		t.Fatalf("TrayLine() = %q", got)
	}
}

func TestCreditPoolAsksForACheckOnceObservedCreditsExpire(t *testing.T) {
	utc(t)
	observed := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := observed.AddDate(0, 0, 11)
	// The review's case: $100 expiring Oct 11, $10 expiring Oct 21, $1/day.
	pool := NewCreditPool("Org", "USD", 11000, 0, observed.AddDate(0, 1, 0),
		[]CreditGrant{{Remaining: 10000, ExpiresAt: observed.AddDate(0, 0, 10)}, {Remaining: 1000, ExpiresAt: observed.AddDate(0, 0, 20)}},
		[]DailySpend{{Day: observed, Amount: 100}}, observed, now)
	if !pool.ExpiredSinceCheck.Equal(observed.AddDate(0, 0, 10)) || !pool.ExpiresAt.IsZero() || !pool.RunsOutAt.IsZero() || pool.ProjectedSpend != 0 {
		t.Fatalf("pool = %+v, want only the expiry since the check", pool)
	}
	if got, want := pool.Summary(now), "credits expired Oct 11 00:00, after the last check · check again"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
	if got := pool.TrayLine(now); got != pool.Summary(now) {
		t.Fatalf("TrayLine() = %q", got)
	}
}

func TestCreditPoolDropsMonthSpendAfterTheMonthEnds(t *testing.T) {
	utc(t)
	observed := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	pool := NewCreditPool("Org", "USD", 5000, 300, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), []CreditGrant{{Remaining: 5000}}, nil, observed, now)
	if pool.Balance != 5000 || !pool.ExpiresAt.IsZero() || pool.MonthSpend != nil {
		t.Fatalf("pool = %+v, want no expiry and no month spend", pool)
	}
	if got := pool.Summary(now); !strings.HasPrefix(got, "$50.00 left · checked ") {
		t.Fatalf("Summary() = %q", got)
	}
}

func TestCreditPoolJSONOmitsUnknownTimes(t *testing.T) {
	observed := time.Date(2026, 10, 8, 17, 0, 0, 0, time.UTC)
	pool := NewCreditPool("Org", "USD", 500, 0, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), nil, nil, observed, observed)
	data, err := json.Marshal(pool)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Contains(got, "0001-01-01") || strings.Contains(got, "expires_at") || strings.Contains(got, "runs_out_at") || !strings.Contains(got, `"balance":500`) {
		t.Fatalf("json = %s", got)
	}
}
