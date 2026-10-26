package provider

import (
	"encoding/json"
	"math"
	"time"
)

// UsageCreditGrant is a non-renewing allowance. Money is in major currency
// units, as reported; nil means the provider did not report that amount.
// Its expiry must never become a quota reset or a forecast period.
type UsageCreditGrant struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Scope       string    `json:"scope"`
	Currency    string    `json:"currency"`
	Used        *float64  `json:"used,omitempty"`
	Limit       *float64  `json:"limit,omitempty"`
	Utilization *float64  `json:"utilization,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitzero"`
}

// CreditAmount copies a finite, nonnegative observation. In particular, an
// omitted amount stays unknown instead of turning into a zero-dollar balance.
func CreditAmount(value *float64) *float64 {
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	copy := *value
	return &copy
}

// UnmarshalJSON also repairs the old cache representation of cloud credits.
// Legacy caches preserve their observed percentage and expiry, but do not
// contain dollar amounts; the migration must not assume a $250 grant.
func (u *UsageData) UnmarshalJSON(data []byte) error {
	type wire UsageData
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*u = UsageData(decoded)
	if u.Provider != "claude" {
		return nil
	}
	var windows []UsageWindow
	for _, window := range u.Windows {
		if window.Name != "bonus" {
			windows = append(windows, window)
			continue
		}
		found := false
		for _, grant := range u.CreditGrants {
			found = found || grant.Name == "cloud_sessions"
		}
		if !found {
			u.CreditGrants = append(u.CreditGrants, UsageCreditGrant{
				Name: "cloud_sessions", DisplayName: "Cloud session credits",
				Scope: "cloud_sessions", Currency: "USD",
				Utilization: CreditAmount(&window.Utilization), ExpiresAt: window.ResetsAt,
			})
		}
	}
	if len(windows) != len(u.Windows) {
		u.Windows = windows
	}
	return nil
}
