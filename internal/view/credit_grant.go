package view

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// CreditGrant is an observed allowance evaluated at the snapshot's clock.
// It never participates in quota ranking, pinning, pacing or reset forecasts.
type CreditGrant struct {
	Name                  string    `json:"name"`
	Label                 string    `json:"label"`
	Scope                 string    `json:"scope"`
	Currency              string    `json:"currency"`
	State                 string    `json:"state"`
	Used                  *float64  `json:"used,omitempty"`
	Limit                 *float64  `json:"limit,omitempty"`
	Remaining             *float64  `json:"remaining,omitempty"`
	LastObservedRemaining *float64  `json:"last_observed_remaining,omitempty"`
	Utilization           *float64  `json:"utilization,omitempty"`
	ExpiresAt             time.Time `json:"expires_at,omitzero"`
}

func buildCreditGrant(raw provider.UsageCreditGrant, reading Reading, at time.Time, maxAge time.Duration) CreditGrant {
	g := CreditGrant{
		Name: raw.Name, Label: raw.DisplayName, Scope: raw.Scope, Currency: raw.Currency,
		Used: provider.CreditAmount(raw.Used), Limit: provider.CreditAmount(raw.Limit),
		Utilization: provider.CreditAmount(raw.Utilization), ExpiresAt: raw.ExpiresAt,
		State: "unknown",
	}
	if g.Label == "" {
		g.Label = g.Name
	}
	if g.Used != nil && g.Limit != nil {
		remaining := math.Max(0, *g.Limit-*g.Used)
		g.Remaining = &remaining
		g.State = "available"
		if remaining == 0 {
			g.State = "depleted"
		}
	}
	age := at.Sub(reading.ObservedAt)
	if reading.State != ReadingFresh || reading.ObservedAt.IsZero() || age < 0 || age > maxAge {
		g.State = "stale"
	}
	if !g.ExpiresAt.IsZero() && !at.Before(g.ExpiresAt) {
		g.State = "expired"
	}
	if g.State == "stale" || g.State == "expired" {
		g.LastObservedRemaining, g.Remaining = g.Remaining, nil
	}
	return g
}

// Summary is shared by the tray and full human CLI views. Expired balances
// remain observations and are never presented as currently spendable credit.
func (g CreditGrant) Summary() string {
	var facts []string
	balance := g.Remaining
	if g.State == "stale" {
		balance = g.LastObservedRemaining
	}
	switch {
	case g.State == "expired":
		facts = append(facts, "expired "+g.ExpiresAt.Local().Format("Jan 2 15:04"))
		if g.LastObservedRemaining != nil {
			facts = append(facts, g.money(*g.LastObservedRemaining)+" left at last check")
		}
	case balance != nil:
		words := g.money(*balance) + " of " + g.money(*g.Limit) + " left"
		if g.State == "stale" {
			words = "last known " + words
		}
		facts = append(facts, words)
	case g.Used != nil:
		facts = append(facts, g.money(*g.Used)+" used", "remaining amount not reported")
	case g.Limit != nil:
		facts = append(facts, g.money(*g.Limit)+" granted", "remaining amount not reported")
		if g.Utilization != nil {
			facts = append(facts, fmt.Sprintf("%.0f%% used", *g.Utilization))
		}
	case g.Utilization != nil:
		facts = append(facts, fmt.Sprintf("%.0f%% used", *g.Utilization), "amount not reported")
	default:
		facts = append(facts, "balance not reported")
	}
	if g.State != "expired" {
		if g.ExpiresAt.IsZero() {
			facts = append(facts, "expiry not reported")
		} else {
			facts = append(facts, "expires "+g.ExpiresAt.Local().Format("Jan 2 15:04"))
		}
	}
	return g.Label + ": " + strings.Join(facts, " · ")
}

func (g CreditGrant) money(value float64) string {
	if g.Currency == "USD" {
		return fmt.Sprintf("$%.2f", value)
	}
	return fmt.Sprintf("%.2f %s", value, g.Currency)
}
