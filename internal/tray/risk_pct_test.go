package tray

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/view"
)

func TestRiskPctCapsOnlyNonFreshProjection(t *testing.T) {
	projected := view.Window{Used: 80, Forecast: view.Forecast{State: view.ForecastKnown, Pct: 150}}
	if got := riskPct(projected, true); got != 150 {
		t.Fatalf("fresh projection risk = %v, want 150", got)
	}
	if got := riskPct(projected, false); got != nonFreshRiskCap {
		t.Fatalf("retained projection risk = %v, want %v", got, nonFreshRiskCap)
	}
	under := view.Window{Used: 40, Forecast: view.Forecast{State: view.ForecastKnown, Pct: 60}}
	if got := riskPct(under, false); got != 60 {
		t.Fatalf("retained projection under the cap = %v, want 60", got)
	}
	warming := view.Window{Used: 40, Forecast: view.Forecast{State: view.ForecastWarming}}
	if got := riskPct(warming, true); got != 0 {
		t.Fatalf("warming forecast risk = %v, want 0", got)
	}
	out := view.Window{Used: 100, Exhaustion: view.Exhaustion{State: view.ExhaustionOut}}
	if got := riskPct(out, true); got != 100 {
		t.Fatalf("exhausted window risk = %v, want its use 100", got)
	}
}
