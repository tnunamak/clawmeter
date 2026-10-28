package contract

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cli"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// The view object is optional in v1: older fixtures stay valid, and the
// view-bearing fixtures and emitted output must match the published schema.

func TestStatusV1ViewFixturesMatchSchemaAndGoContract(t *testing.T) {
	schema := compileSchema(t, "status-v1.schema.json")
	for _, name := range []string{"status-v1-view-success.json", "status-v1-view-partial-error.json"} {
		data := readRepoFile(t, "testdata", "contracts", name)
		var raw any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := schema.Validate(raw); err != nil {
			t.Fatalf("%s does not match published schema: %v", name, err)
		}
		var output cli.JSONOutput
		if err := json.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		if output.SchemaVersion != cli.JSONSchemaVersion || output.View == nil || len(output.View.Accounts) == 0 {
			t.Fatalf("%s: missing top-level view: %#v", name, output.View)
		}
		for family, p := range output.Providers {
			if p.View == nil && len(p.Sources) == 0 {
				t.Fatalf("%s: provider %s has no view", name, family)
			}
		}
	}
}

func TestEmittedStatusWithViewMatchesPublishedSchema(t *testing.T) {
	now := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
	output := &cli.MultiProviderOutput{Providers: []cli.ProviderFormatter{
		{
			Name: "openai", Display: "Codex", DisplayName: "Codex", Family: "openai", SourceID: "default", SourceCount: 1,
			Data: &provider.UsageData{Provider: "openai", FetchedAt: now, Windows: []provider.UsageWindow{
				{Name: "7d", Utilization: 58, ResetsAt: now.Add(6 * 24 * time.Hour)},
				{Name: "5h Sonnet", Utilization: 0},
			}},
		},
		{
			Name: "claude", Display: "Claude", DisplayName: "Claude", Family: "claude", SourceID: "default", SourceCount: 1,
			Data: &provider.UsageData{Provider: "claude", FetchedAt: now, Stale: true, Warning: "rate limited",
				Windows: []provider.UsageWindow{{Name: "5h", Utilization: 100, ResetsAt: now.Add(time.Hour)}}},
		},
		{
			Name: "gemini", Display: "Gemini", DisplayName: "Gemini", Family: "gemini", SourceID: "default", SourceCount: 1,
			Data: &provider.UsageData{Provider: "gemini", FetchedAt: now, Error: "rate limited (429)"},
		},
	}}
	data, err := output.JSON(nil, now)
	if err != nil {
		t.Fatal(err)
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if err := compileSchema(t, "status-v1.schema.json").Validate(raw); err != nil {
		t.Fatalf("emitted status does not match published schema: %v\n%s", err, data)
	}
	var emitted cli.JSONOutput
	if err := json.Unmarshal(data, &emitted); err != nil {
		t.Fatal(err)
	}
	forecast := emitted.Providers["openai"].View.Windows[0].Forecast
	if forecast.Basis != "average_since_reset" || forecast.Pct == nil {
		t.Fatalf("known forecast omitted its model: %+v", forecast)
	}
}

func TestStatusSchemaKeepsViewValuesKnownAndOptionalWithoutVersionBump(t *testing.T) {
	schema := compileSchema(t, "status-v1.schema.json")
	base := func() map[string]any {
		var doc map[string]any
		if err := json.Unmarshal(readRepoFile(t, "testdata", "contracts", "status-v1-view-success.json"), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	openaiWindow := func(doc map[string]any) map[string]any {
		providers := doc["providers"].(map[string]any)
		view := providers["openai"].(map[string]any)["view"].(map[string]any)
		return view["windows"].([]any)[0].(map[string]any)
	}

	if err := schema.Validate(base()); err != nil {
		t.Fatalf("success fixture rejected: %v", err)
	}

	// A v1 document without any view is still valid.
	legacy := base()
	delete(legacy, "view")
	for _, p := range legacy["providers"].(map[string]any) {
		delete(p.(map[string]any), "view")
	}
	if err := schema.Validate(legacy); err != nil {
		t.Fatalf("pre-view document rejected: %v", err)
	}

	cases := map[string]func(map[string]any){
		"unknown forecast basis": func(doc map[string]any) {
			openaiWindow(doc)["forecast"].(map[string]any)["basis"] = "recent_demand"
		},
		"unknown forecast state": func(doc map[string]any) {
			openaiWindow(doc)["forecast"].(map[string]any)["state"] = "invented"
		},
		"pct on a warming forecast": func(doc map[string]any) {
			forecast := openaiWindow(doc)["forecast"].(map[string]any)
			forecast["state"] = "warming"
		},
		"run-out ETA on a window that lasts": func(doc map[string]any) {
			exhaustion := openaiWindow(doc)["exhaustion"].(map[string]any)
			exhaustion["state"] = "lasts"
		},
		"runs_out without its ETA": func(doc map[string]any) {
			exhaustion := openaiWindow(doc)["exhaustion"].(map[string]any)
			delete(exhaustion, "runs_out_in_seconds")
		},
		"reset time while the reset is unknown": func(doc map[string]any) {
			reset := openaiWindow(doc)["reset"].(map[string]any)
			reset["known"] = false
		},
		"worst without window": func(doc map[string]any) {
			delete(doc["view"].(map[string]any)["worst"].(map[string]any), "window")
		},
	}
	for name, mutate := range cases {
		doc := base()
		mutate(doc)
		if err := schema.Validate(doc); err == nil {
			t.Errorf("%s: schema accepted an invalid view", name)
		}
	}
}
