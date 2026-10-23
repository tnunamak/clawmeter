package provider

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// UsageCreditPool is a prepaid API credit balance observed in a provider's
// console. Amounts are minor units (cents) of Currency. Every value comes
// from the console snapshot taken at ObservedAt; the projection fields extend
// the observed burn rate from that moment, so they are labelled projections.
type UsageCreditPool struct {
	// Name labels the pool where it shows: its organization on the Claude API
	// row, "API credits" when folded under the Claude source whose plan funds
	// it.
	Name         string `json:"name"`
	Organization string `json:"organization"`
	// Source is the Claude source key ("claude", "claude:odl") whose plan
	// funds this pool, verified against that source's current organization.
	Source        string `json:"claude_source,omitempty"`
	Plan          string `json:"plan,omitempty"`
	MonthlyCredit int64  `json:"monthly_credit,omitempty"`
	Currency      string `json:"currency"`
	Balance       int64  `json:"balance"`
	// MonthSpend is this billing month's spend; nil once the month observed
	// in the snapshot has ended.
	MonthSpend    *int64    `json:"month_spend,omitempty"`
	MonthResetsAt time.Time `json:"month_resets_at"`
	// ExpiresAt is the next time credits in this pool expire, and Expiring
	// the observed balance of the grants that expire then.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Expiring  int64     `json:"expiring,omitempty"`
	// BurnPerDay is the average daily spend over BurnDays (at most 7) of
	// console cost data ending at ObservedAt.
	BurnPerDay        float64   `json:"burn_per_day"`
	BurnDays          int       `json:"burn_days"`
	ObservedAt        time.Time `json:"observed_at"`
	Live              bool      `json:"live"`
	ObservationSource string    `json:"observation_source,omitempty"`
	SignedOut         bool      `json:"signed_out,omitempty"`
	// ExtensionContactAt is when the browser extension last reached
	// Clawmeter. With it, a stale extension reading tells a signed-in browser
	// that could not read this organization from a browser that is not running.
	ExtensionContactAt time.Time `json:"extension_contact_at,omitempty"`
	// Projection from ObservedAt at BurnPerDay, set only with ExpiresAt.
	ProjectedSpend  int64     `json:"projected_spend_before_expiry,omitempty"`
	ProjectedUnused int64     `json:"projected_unused_at_expiry,omitempty"`
	RunsOutAt       time.Time `json:"runs_out_at,omitempty"`
	// NextCreditAt is when the plan funding this pool next grants its monthly
	// credit. It is set only for a linked pool.
	NextCreditAt time.Time `json:"next_credit_at,omitempty"`
	// ExpiredSinceCheck is set when credits seen in the snapshot have since
	// expired. The snapshot no longer describes the pool, so it carries no
	// expiry or projection until the next check.
	ExpiredSinceCheck time.Time `json:"expired_since_check,omitempty"`
}

// The step that clears a pool's warning, and the window in which a recent
// extension contact means the browser is running.
const (
	signInAction           = "sign in to the Claude Console"
	updateAction           = "open your browser to update"
	consoleAction          = "open the Claude Console"
	extensionContactWindow = 3 * time.Minute
)

// CreditGrant is one tranche of a pool, as observed.
type CreditGrant struct {
	Remaining int64
	ExpiresAt time.Time // zero when it never expires
}

// DailySpend is console-reported spend for one UTC day, in fractional minor
// units.
type DailySpend struct {
	Day    time.Time
	Amount float64
}

const burnWindowDays = 7

// NewCreditPool derives burn rate, next expiry and projection from observed
// console data. Spend is assumed to draw the earliest-expiring credits first.
// Once an observed grant expires, how much of it was spent first is unknown,
// so the pool reports ExpiredSinceCheck instead of a guessed balance.
func NewCreditPool(name, currency string, balance, monthSpend int64, monthResetsAt time.Time, grants []CreditGrant, daily []DailySpend, observedAt, now time.Time) UsageCreditPool {
	pool := UsageCreditPool{Name: name, Organization: name, Currency: currency, Balance: balance, MonthResetsAt: monthResetsAt, ObservedAt: observedAt}
	if now.Before(monthResetsAt) {
		pool.MonthSpend = &monthSpend
	}
	for _, grant := range grants {
		if grant.ExpiresAt.IsZero() || grant.Remaining <= 0 {
			continue
		}
		if !grant.ExpiresAt.After(now) {
			if pool.ExpiredSinceCheck.IsZero() || grant.ExpiresAt.After(pool.ExpiredSinceCheck) {
				pool.ExpiredSinceCheck = grant.ExpiresAt
			}
			continue
		}
		switch {
		case pool.ExpiresAt.IsZero() || grant.ExpiresAt.Before(pool.ExpiresAt):
			pool.ExpiresAt, pool.Expiring = grant.ExpiresAt, grant.Remaining
		case grant.ExpiresAt.Equal(pool.ExpiresAt):
			pool.Expiring += grant.Remaining
		}
	}
	pool.Expiring = max(min(pool.Expiring, pool.Balance), 0)
	pool.BurnPerDay, pool.BurnDays = burnRate(daily, observedAt)
	if !pool.ExpiredSinceCheck.IsZero() {
		pool.ExpiresAt, pool.Expiring = time.Time{}, 0
		return pool
	}
	// An empty or unpaid pool has nothing left to run out or expire.
	if pool.ExpiresAt.IsZero() || pool.Balance <= 0 {
		return pool
	}
	days := pool.ExpiresAt.Sub(observedAt).Hours() / 24
	spend := int64(math.Round(pool.BurnPerDay * max(days, 0)))
	if spend >= pool.Balance && pool.BurnPerDay > 0 {
		spend = pool.Balance
		pool.RunsOutAt = observedAt.Add(time.Duration(float64(pool.Balance) / pool.BurnPerDay * 24 * float64(time.Hour)))
	}
	pool.ProjectedSpend = spend
	pool.ProjectedUnused = max(pool.Expiring-spend, 0)
	return pool
}

// burnRate averages spend from the first day with spend (at most
// burnWindowDays back, today included) through observedAt. The span is at
// least one day, so a few early requests are not extrapolated into a large
// daily rate.
func burnRate(daily []DailySpend, observedAt time.Time) (float64, int) {
	today := observedAt.UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(burnWindowDays - 1))
	first, total := time.Time{}, 0.0
	for _, day := range daily {
		if day.Day.Before(start) || day.Day.After(today) || day.Amount <= 0 {
			continue
		}
		total += day.Amount
		if first.IsZero() || day.Day.Before(first) {
			first = day.Day
		}
	}
	if first.IsZero() {
		return 0, burnWindowDays
	}
	span := max(observedAt.Sub(first).Hours()/24, 1)
	return total / span, int(math.Ceil(span))
}

// Summary is the one-line pool status, for example
// "$199.88 left · spent $0.11 this month · est. $1.84 more spent before it expires Oct 24 19:00 · checked 12:01".
// A projected amount is an estimate and says so; an observed amount does not.
func (p UsageCreditPool) Summary(now time.Time) string {
	if !p.ExpiredSinceCheck.IsZero() {
		return p.expiredLine(now)
	}
	parts := []string{p.balanceText()}
	if p.MonthSpend != nil {
		parts = append(parts, "spent "+formatMinorUnits(int(*p.MonthSpend), p.Currency)+" this month")
	}
	switch {
	case p.ExpiresAt.IsZero():
		if p.BurnPerDay > 0 {
			parts = append(parts, formatMinorUnits(int(math.Round(p.BurnPerDay)), p.Currency)+"/day")
		}
	case p.BurnPerDay == 0:
		parts = append(parts, "no spend in 7 days, expires "+expiryTime(p.ExpiresAt, now))
	case !p.RunsOutAt.IsZero():
		parts = append(parts, "runs out "+shortDate(p.RunsOutAt, now)+", before it expires "+expiryTime(p.ExpiresAt, now))
	default:
		parts = append(parts, "est. "+formatMinorUnits(int(p.ProjectedSpend), p.Currency)+" more spent before it expires "+expiryTime(p.ExpiresAt, now))
	}
	if next := p.nextCreditText(now); next != "" {
		parts = append(parts, next)
	}
	if freshness := p.freshness(now); freshness != "" {
		parts = append(parts, freshness)
	}
	return strings.Join(parts, " · ")
}

// TrayLine is the short pool status for a menu row, for example
// "$199.88 left · est. $198.04 unused at expiry Oct 24 19:00 · checked 12:01".
func (p UsageCreditPool) TrayLine(now time.Time) string {
	if !p.ExpiredSinceCheck.IsZero() {
		return p.expiredLine(now)
	}
	parts := []string{p.balanceText()}
	switch {
	case !p.RunsOutAt.IsZero():
		parts = append(parts, "runs out "+shortDate(p.RunsOutAt, now))
	case !p.ExpiresAt.IsZero():
		parts = append(parts, "est. "+formatMinorUnits(int(p.ProjectedUnused), p.Currency)+" unused at expiry "+expiryTime(p.ExpiresAt, now))
	}
	if next := p.nextCreditText(now); next != "" {
		parts = append(parts, next)
	}
	if freshness := p.freshness(now); freshness != "" {
		parts = append(parts, freshness)
	}
	return strings.Join(parts, " · ")
}

// balanceText is "$199.88 left", or "$4.50 unpaid · add funds" once spend
// has passed the credits. The Console stops API access until it is paid.
func (p UsageCreditPool) balanceText() string {
	if p.Balance < 0 {
		return formatMinorUnits(int(-p.Balance), p.Currency) + " unpaid · add funds"
	}
	return formatMinorUnits(int(p.Balance), p.Currency) + " left"
}

// nextCreditText is "next $200.00 credit Oct 24 00:50", or "next credit Oct 24
// 00:50" when the monthly amount is unknown. It is empty once the date has passed.
func (p UsageCreditPool) nextCreditText(now time.Time) string {
	if p.NextCreditAt.IsZero() || !p.NextCreditAt.After(now) {
		return ""
	}
	label := "next credit"
	if p.MonthlyCredit > 0 {
		label = "next " + formatMinorUnits(int(p.MonthlyCredit), p.Currency) + " credit"
	}
	return label + " " + expiryTime(p.NextCreditAt, now)
}

// IsLive also checks age when a cached pool is rendered.
func (p UsageCreditPool) IsLive(now time.Time) bool {
	return p.Live && !p.SignedOut && now.Sub(p.ObservedAt) <= 5*time.Minute
}

func (p UsageCreditPool) freshness(now time.Time) string {
	if p.ObservationSource != "extension" {
		return "checked " + shortDate(p.ObservedAt, now)
	}
	if p.SignedOut {
		return p.Action(now)
	}
	if p.IsLive(now) {
		return ""
	}
	stale := "not updated since " + shortDate(p.ObservedAt, now)
	if action := p.Action(now); action != "" {
		return stale + " · " + action
	}
	return stale
}

// Action is the step that clears this pool's warning at now, or "" when it
// needs none. It is chosen when the pool is drawn, because a reading that was
// live at fetch time can be stale by then.
func (p UsageCreditPool) Action(now time.Time) string {
	if p.ObservationSource != "extension" {
		if !p.ExpiredSinceCheck.IsZero() {
			return consoleAction
		}
		return ""
	}
	switch {
	case p.SignedOut:
		return signInAction
	case p.IsLive(now):
		return ""
	case !p.ExtensionContactAt.IsZero() && now.Sub(p.ExtensionContactAt) <= extensionContactWindow:
		return signInAction
	default:
		return updateAction
	}
}

// expiryTime shows the local date and time. Consoles often show expiry as a
// UTC date, which can be a day later than the local date; the time makes the
// two readings agree.
func expiryTime(t, now time.Time) string {
	if t.Local().Year() == now.Local().Year() && t.Local().YearDay() == now.Local().YearDay() {
		return t.Local().Format("15:04")
	}
	return shortDate(t, now) + " " + t.Local().Format("15:04")
}

func (p UsageCreditPool) expiredLine(now time.Time) string {
	line := "credits expired " + expiryTime(p.ExpiredSinceCheck, now) + ", after the last check"
	if p.ObservationSource == "extension" {
		if freshness := p.freshness(now); freshness != "" {
			return line + " · " + freshness
		}
		return line
	}
	if action := p.Action(now); action != "" {
		return line + " · " + action
	}
	return line
}

// MarshalJSON omits unknown times instead of serializing Go's zero time.
func (p UsageCreditPool) MarshalJSON() ([]byte, error) {
	type pool UsageCreditPool
	out := struct {
		pool
		ExpiresAt          *time.Time `json:"expires_at,omitempty"`
		RunsOutAt          *time.Time `json:"runs_out_at,omitempty"`
		NextCreditAt       *time.Time `json:"next_credit_at,omitempty"`
		ExtensionContactAt *time.Time `json:"extension_contact_at,omitempty"`
		ExpiredSinceCheck  *time.Time `json:"expired_since_check,omitempty"`
	}{pool: pool(p)}
	if !p.ExpiredSinceCheck.IsZero() {
		out.ExpiredSinceCheck = &p.ExpiredSinceCheck
	}
	if !p.ExpiresAt.IsZero() {
		out.ExpiresAt = &p.ExpiresAt
	}
	if !p.RunsOutAt.IsZero() {
		out.RunsOutAt = &p.RunsOutAt
	}
	if !p.NextCreditAt.IsZero() {
		out.NextCreditAt = &p.NextCreditAt
	}
	if !p.ExtensionContactAt.IsZero() {
		out.ExtensionContactAt = &p.ExtensionContactAt
	}
	return json.Marshal(out)
}
