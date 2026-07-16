package contract

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tnunamak/clawmeter/internal/cli"
	"github.com/tnunamak/clawmeter/internal/diagnose"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestPublishedSchemasAreJSON(t *testing.T) {
	for _, name := range []string{"status-v1.schema.json", "diagnose-v1.schema.json"} {
		data := readRepoFile(t, "docs", "schemas", name)
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Fatalf("%s: unexpected or missing $schema", name)
		}
	}
}

func TestStatusV1FixturesMatchGoContract(t *testing.T) {
	schema := compileSchema(t, "status-v1.schema.json")
	for _, name := range []string{"status-v1-healthy.json", "status-v1-partial-error.json"} {
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
		if output.SchemaVersion != cli.JSONSchemaVersion || len(output.Providers) == 0 {
			t.Fatalf("%s: invalid contract spine: %#v", name, output)
		}
	}
}

func TestDiagnoseV1FixturesMatchGoContract(t *testing.T) {
	schema := compileSchema(t, "diagnose-v1.schema.json")
	for _, name := range []string{"diagnose-v1-success.json", "diagnose-v1-safe-error.json"} {
		data := readRepoFile(t, "testdata", "contracts", name)
		var raw any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := schema.Validate(raw); err != nil {
			t.Fatalf("%s does not match published schema: %v", name, err)
		}
		var output diagnose.Output
		if err := json.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		if output.SchemaVersion != diagnose.SchemaVersion || len(output.Diagnostics) == 0 {
			t.Fatalf("%s: invalid contract spine: %#v", name, output)
		}
	}
}

func TestEmittedDiagnosticsMatchPublishedSchema(t *testing.T) {
	now := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
	providers := []provider.Provider{
		&contractProvider{name: "success", ready: true, data: &provider.UsageData{
			Provider: "success", FetchedAt: now,
			Windows: []provider.UsageWindow{{Name: "7d", Utilization: 25, ResetsAt: now.Add(time.Hour)}},
		}},
		&contractProvider{name: "error", ready: true, err: errors.New("HTTP 401 account@example.com")},
		&contractProvider{name: "skipped", ready: false},
	}
	output := diagnose.Run(
		context.Background(), providers,
		func(string) string { return "detected" },
		func(name string) bool { return name != "skipped" },
	)
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if err := compileSchema(t, "diagnose-v1.schema.json").Validate(raw); err != nil {
		t.Fatalf("emitted diagnostic does not match published schema: %v\n%s", err, data)
	}
}

func TestDiagnoseSchemaRejectsBrokenSpineButAllowsExtensions(t *testing.T) {
	schema := compileSchema(t, "diagnose-v1.schema.json")
	base := map[string]any{
		"schema_version": 1,
		"generated_at":   "2026-07-16T18:00:00Z",
		"diagnostics": []any{map[string]any{
			"provider":      "openai",
			"maturity":      map[string]any{"experimental": false},
			"setup":         map[string]any{"state": "ready"},
			"polling_state": "detected",
			"probe":         map[string]any{"attempted": true, "outcome": "success"},
		}},
	}
	if err := schema.Validate(base); err != nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
	base["future_optional_field"] = true
	if err := schema.Validate(base); err != nil {
		t.Fatalf("additive extension rejected: %v", err)
	}
	base["diagnostics"] = []any{}
	if err := schema.Validate(base); err == nil {
		t.Fatal("empty diagnostic batch should be rejected")
	}
	base["diagnostics"] = []any{map[string]any{
		"provider":      "openai",
		"maturity":      map[string]any{"experimental": false},
		"setup":         map[string]any{"state": "ready"},
		"polling_state": "detected",
		"probe":         map[string]any{"attempted": true, "outcome": "invented"},
	}}
	if err := schema.Validate(base); err == nil {
		t.Fatal("unknown closed-enum value should be rejected")
	}
}

type contractProvider struct {
	name  string
	ready bool
	data  *provider.UsageData
	err   error
}

func (p *contractProvider) Name() string         { return p.name }
func (p *contractProvider) DisplayName() string  { return p.name }
func (p *contractProvider) Description() string  { return "test source" }
func (p *contractProvider) DashboardURL() string { return "" }
func (p *contractProvider) IsConfigured() bool   { return p.ready }
func (p *contractProvider) FetchUsage(context.Context) (*provider.UsageData, error) {
	return p.data, p.err
}
func (p *contractProvider) SetupStatus() provider.SetupStatus {
	if p.ready {
		return provider.SetupStatus{State: provider.SetupReady}
	}
	return provider.SetupStatus{State: provider.SetupNeedsAuth}
}

func compileSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	location := "file:///" + name
	var document any
	if err := json.Unmarshal(readRepoFile(t, "docs", "schemas", name), &document); err != nil {
		t.Fatal(err)
	}
	if err := compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func readRepoFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
