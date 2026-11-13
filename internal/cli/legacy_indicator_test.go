package cli

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/view"
)

// The legacy indicator is a v1 value. A reset-passed window has always reported
// "stale" there, and the pending state must not change that for consumers.
func TestLegacyIndicatorStillSaysStaleForPendingForecast(t *testing.T) {
	acct := &view.Account{Windows: []view.Window{{
		Name:     "5h",
		Reset:    view.Reset{Known: true},
		Forecast: view.Forecast{State: view.ForecastPending},
	}}}
	got := forecastFor(acct)
	if got == nil || got.Windows["5h"].Indicator != "stale" {
		t.Fatalf("legacy forecast = %+v; want the stale indicator for a pending forecast", got)
	}
}
