package provider

import (
	"strings"
	"time"
)

// UsageAPICreditBalance is a reported balance with dated grant expiries. It
// carries no spend rate: the billing endpoint does not report a burn history.
type UsageAPICreditBalance struct {
	Pool       string            `json:"pool"`
	Name       string            `json:"name"`
	Currency   string            `json:"currency"`
	Balance    int64             `json:"balance"`
	ObservedAt time.Time         `json:"observed_at"`
	Grants     []APICreditExpiry `json:"grants"`
}

type APICreditExpiry struct {
	Remaining int64     `json:"remaining"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func (b UsageAPICreditBalance) State(now time.Time) string {
	for _, grant := range b.Grants {
		if grant.Remaining > 0 && !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now) {
			return "expired_since_check"
		}
	}
	if b.ObservedAt.IsZero() || b.ObservedAt.After(now) || now.Sub(b.ObservedAt) >= 5*time.Minute {
		return "stale"
	}
	return "current"
}

func (b UsageAPICreditBalance) Summary(now time.Time) string {
	prefix := b.Name + ": "
	switch b.State(now) {
	case "expired_since_check":
		return prefix + "credits expired since last check · Open OpenAI billing…"
	case "stale":
		return prefix + "last checked balance " + formatMinorUnits(int(b.Balance), b.Currency) + " at " + shortDate(b.ObservedAt, now) + " · Open OpenAI billing…"
	}
	parts := []string{prefix + formatMinorUnits(int(b.Balance), b.Currency) + " API credits left"}
	if b.Balance < 0 {
		parts[0] = prefix + formatMinorUnits(int(-b.Balance), b.Currency) + " API balance owed"
	}
	var expiry time.Time
	for _, grant := range b.Grants {
		if grant.Remaining > 0 && !grant.ExpiresAt.IsZero() && (expiry.IsZero() || grant.ExpiresAt.Before(expiry)) {
			expiry = grant.ExpiresAt
		}
	}
	if !expiry.IsZero() {
		parts = append(parts, "next expiry "+expiryTime(expiry, now))
	}
	return strings.Join(parts, " · ")
}
