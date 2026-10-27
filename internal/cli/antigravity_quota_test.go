package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray"
)

func TestDynamicQuotaAndForecastReasonAgreeAcrossSurfaces(t *testing.T) {
	at := matrixNow
	data := &provider.UsageData{Provider: "antigravity", FetchedAt: at, Windows: []provider.UsageWindow{
		{Name: "future-pool", DisplayName: "7 days (Future reasoning)", Scope: "Models sharing this pool: Future Model", Length: 7 * 24 * time.Hour, ResetsAt: at.Add(7 * 24 * time.Hour)},
	}}
	m := &MultiProviderOutput{Providers: []ProviderFormatter{{Name: "antigravity", DisplayName: "Antigravity", Data: data}}}
	v := m.buildView(at)
	for name, text := range map[string]string{"plain": v.plainLine(0), "table": strings.Join(v.colorTable(), "\n"), "tray": strings.Join(tray.MenuRows(v.snap, 8)["antigravity"], "\n")} {
		if !strings.Contains(text, "no usage yet") || strings.Contains(text, "forecast soon") {
			t.Fatalf("%s=%s", name, text)
		}
	}
	agent := v.agentLine()
	if !strings.Contains(agent, "forecast_reason=no_usage") || !strings.Contains(agent, "Future Model") {
		t.Fatalf("agent=%s", agent)
	}
	b, err := v.jsonDocument(nil, at)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Providers map[string]struct {
			View struct {
				Windows []struct {
					Scope    string `json:"scope"`
					Forecast struct {
						State, Reason string
						Pct           *float64
					}
				}
			}
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	w := doc.Providers["antigravity"].View.Windows[0]
	if w.Forecast.State != "warming" || w.Forecast.Reason != "no_usage" || w.Forecast.Pct != nil || w.Scope != data.Windows[0].Scope {
		t.Fatalf("JSON=%s", b)
	}
}
