package cli

import (
	"github.com/tnunamak/clawmeter/internal/provider"
	"strings"
	"testing"
	"time"
)

func TestLocalModelsVisibleWithoutQuotaClaims(t *testing.T) {
	n := int64(0)
	at := matrixNow
	data := &provider.UsageData{Provider: "opencode_free", FetchedAt: at, LocalModels: []provider.LocalModelUsage{{Model: "future-free", Label: "Future Free", Day: at.UTC().Format("2006-01-02"), AssistantMessages: &n}}}
	m := &MultiProviderOutput{Providers: []ProviderFormatter{{Name: "opencode_free", DisplayName: "OpenCode Free", Data: data}}}
	v := m.buildView(at)
	for name, s := range map[string]string{"plain": v.plainLine(0), "table": strings.Join(v.colorTable(), "\n"), "agent": v.agentLine()} {
		if !strings.Contains(s, "Future Free") {
			t.Fatalf("%s hides unused model: %s", name, s)
		}
		if strings.Contains(s, "100%") || strings.Contains(s, "on_track") || strings.Contains(s, "available") {
			t.Fatalf("%s invents quota: %s", name, s)
		}
	}
	if v.snap.Worst != nil || len(v.snap.Accounts[0].Windows) != 0 {
		t.Fatal("local observations ranked as quota")
	}
	b, err := v.jsonDocument(nil, at)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"local_models"`) != 2 || !strings.Contains(string(b), `"assistant_messages": 0`) {
		t.Fatalf("raw and evaluated JSON lost observations: %s", b)
	}
	later := m.buildView(at.Add(11 * time.Minute))
	if later.snap.Accounts[0].LocalModels[0].AssistantMessages != nil {
		t.Fatal("old cache advertised current activity")
	}
}
