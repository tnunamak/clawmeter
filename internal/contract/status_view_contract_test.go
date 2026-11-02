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
	// Basis is optional even in a known, view-bearing v1 forecast.
	doc := raw.(map[string]any)
	providers := doc["providers"].(map[string]any)
	account := providers["openai"].(map[string]any)["view"].(map[string]any)
	known := account["windows"].([]any)[0].(map[string]any)["forecast"].(map[string]any)
	if known["state"] != "known" || known["basis"] != "average_since_reset" || known["pct"] == nil {
		t.Fatalf("expected emitted known forecast before deleting basis: %v", known)
	}
	delete(known, "basis")
	if err := compileSchema(t, "status-v1.schema.json").Validate(doc); err != nil {
		t.Fatalf("known v1 forecast without optional basis rejected: %v", err)
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

// Keep this fixture on the real status encoder: hand-written JSON alone would
// miss custom marshaling, omitted pointers, and evaluated account view fields.
func statusCoverageJSON(t *testing.T) []byte {
	t.Helper()
	now := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	used, limit, pct := 12.5, 100.0, 12.5
	count, cents := int64(3), int64(10000)
	models := []provider.LocalModelUsage{{
		Model: "example", Label: "Example", Day: "2026-07-16",
		AssistantMessages: &count, InputTokens: &count, OutputTokens: &count,
		ReasoningTokens: &count, CacheReadTokens: &count, CacheWriteTokens: &count,
		LimitHitAt: now.Add(-time.Minute), RetryAt: later, ResetAt: later,
	}}
	data := &provider.UsageData{
		Provider: "openai", FetchedAt: now, ResetCreditsTarget: "openai",
		Windows: []provider.UsageWindow{{
			Name: "5h", Scope: "shared", Description: "Shared usage",
			Utilization: pct, Length: 1500 * time.Millisecond, LengthUnknown: true,
			ResetsAt: later, Limit: 100, Used: 12, Currency: "USD",
		}},
		ResetCredits: &provider.UsageResetCredits{
			AvailableCount: 1, EarliestExpiresAt: later, FetchedAt: now,
			Warning: "snapshot", Snapshot: true,
			Credits: []provider.UsageResetCredit{{Status: "available", CreatedAt: now, ExpiresAt: later, ConsumedAt: now}},
		},
		Balances: []provider.UsageBalance{{Name: "balance", DisplayName: "Balance", Total: 100.5, Used: 12.5, Remaining: 88}},
		AnnouncedReset: &provider.ResetAnnouncement{
			Kind: "reset", Official: false, Feed: "example", SourceURL: "https://example.com/reset",
			AnnouncedAt: now, ScheduledFor: later, Approximate: true, Windows: []string{"5h"},
			FeedCheckedAt: now, ShownUntil: later,
		},
		CreditGrants: []provider.UsageCreditGrant{
			{Name: "current", DisplayName: "Current", Scope: "cloud_sessions", Currency: "USD", Used: &used, Limit: &limit, Utilization: &pct, ExpiresAt: later},
			{Name: "expired", DisplayName: "Expired", Scope: "cloud_sessions", Currency: "USD", Used: &used, Limit: &limit, Utilization: &pct, ExpiresAt: now.Add(-time.Minute)},
		},
		CreditBenefits: []provider.UsageCreditBenefit{{
			Name: "offer", Pool: "api", LinkObservedAt: now, Eligibility: "eligible", LinkState: "unlinked",
			Currency: "USD", Claimable: &cents, PeriodCredit: &cents, Cadence: "monthly", ExpiresAt: later, ObservedAt: now,
		}},
		LocalModels: models,
		APICreditBalances: []provider.UsageAPICreditBalance{
			{Pool: "api", Name: "API", Currency: "USD", Balance: cents, ObservedAt: now,
				Grants: []provider.APICreditExpiry{{Remaining: cents, ExpiresAt: later}}},
			{Pool: "no-expiry", Name: "No expiry reported", Currency: "USD", ObservedAt: now},
		},
		CreditPools: []provider.UsageCreditPool{{
			Name: "pool", Organization: "Example", Source: "claude", Plan: "example", MonthlyCredit: cents,
			Currency: "USD", Balance: cents, MonthSpend: &count, MonthResetsAt: later,
			ExpiresAt: later, Expiring: cents, BurnPerDay: 1.5, BurnDays: 7, ObservedAt: now,
			Live: true, ObservationSource: "extension", SignedOut: true, ExtensionContactAt: now,
			ProjectedSpend: count, ProjectedUnused: cents, RunsOutAt: later, NextCreditAt: later, ExpiredSinceCheck: now,
		}},
	}
	output := &cli.MultiProviderOutput{Providers: []cli.ProviderFormatter{
		{Name: "openai", Family: "openai", DisplayName: "Codex", SourceID: "default", SourceCount: 1, Data: data},
		// LocalModelStatus is emitted only for a local-only account, not one
		// that also carries quota windows.
		{Name: "local", Family: "local", DisplayName: "Local", SourceID: "default", SourceCount: 1,
			Data: &provider.UsageData{Provider: "local", FetchedAt: now, LocalModels: models, AnnouncedReset: &provider.ResetAnnouncement{}}},
	}}
	encoded, err := output.JSON(nil, now)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func statusCoverageDocument(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// coverageObject resolves an explicit path, failing if the producer did not
// emit it. Mutation tests must never pass by silently adding a missing field.
func coverageObject(t *testing.T, doc map[string]any, path []any) map[string]any {
	t.Helper()
	var value any = doc
	for _, part := range path {
		switch key := part.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("%v: expected object before %q, got %T", path, key, value)
			}
			value = object[key]
		case int:
			array, ok := value.([]any)
			if !ok || key >= len(array) {
				t.Fatalf("%v: missing array index %d", path, key)
			}
			value = array[key]
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%v: expected object, got %T", path, value)
	}
	return object
}

func TestEmittedStatusOptionalFieldsMatchPublishedSchema(t *testing.T) {
	encoded := statusCoverageJSON(t)
	doc := statusCoverageDocument(t, encoded)
	schema := compileSchema(t, "status-v1.schema.json")
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("rich emitted status rejected: %v\n%s", err, encoded)
	}
	window := coverageObject(t, doc, []any{"providers", "openai", "usage", "windows", 0})
	if window["window_length_seconds"] != 1.5 {
		t.Fatalf("fractional window length lost: %v", window)
	}
	// Optional pointer and omitzero fields are absent, never null. The two
	// non-omitempty nested slices below really can encode null.
	api := coverageObject(t, doc, []any{"providers", "openai", "usage", "api_credit_balances", 1})
	announcement := coverageObject(t, doc, []any{"providers", "local", "usage", "announced_reset"})
	for field, object := range map[string]map[string]any{"grants": api, "windows": announcement} {
		if value, ok := object[field]; !ok || value != nil {
			t.Fatalf("expected emitted null %s, got %v", field, object)
		}
	}
}

func TestStatusSchemaRejectsWrongOptionalFieldTypes(t *testing.T) {
	encoded := statusCoverageJSON(t)
	schema := compileSchema(t, "status-v1.schema.json")
	cases := []struct {
		name   string
		path   []any
		fields []string
	}{
		{"window", []any{"providers", "openai", "usage", "windows", 0}, []string{"scope", "description", "window_length_unknown", "window_length_seconds", "limit", "used", "currency"}},
		{"reset credits", []any{"providers", "openai", "usage", "reset_credits"}, []string{"earliest_expires_at", "credits", "warning", "snapshot"}},
		{"reset credit", []any{"providers", "openai", "usage", "reset_credits", "credits", 0}, []string{"status", "created_at", "expires_at", "consumed_at"}},
		{"usage", []any{"providers", "openai", "usage"}, []string{"reset_credits_target", "balances", "announced_reset", "credit_grants", "credit_benefits", "local_models", "api_credit_balances", "credit_pools"}},
		{"balance", []any{"providers", "openai", "usage", "balances", 0}, []string{"name", "display_name", "total", "used", "remaining"}},
		{"announcement", []any{"providers", "openai", "usage", "announced_reset"}, []string{"kind", "official", "feed", "source_url", "announced_at", "scheduled_for", "approximate", "windows", "feed_checked_at", "shown_until"}},
		{"grant", []any{"providers", "openai", "usage", "credit_grants", 0}, []string{"name", "display_name", "scope", "currency", "used", "limit", "utilization", "expires_at"}},
		{"benefit", []any{"providers", "openai", "usage", "credit_benefits", 0}, []string{"name", "pool", "link_observed_at", "eligibility", "link_state", "currency", "claimable", "period_credit", "cadence", "expires_at", "observed_at"}},
		{"local model", []any{"providers", "openai", "usage", "local_models", 0}, []string{"model", "label", "day_utc", "assistant_messages", "input_tokens", "output_tokens", "reasoning_tokens", "cache_read_tokens", "cache_write_tokens", "limit_hit_at", "retry_hint_at", "reset_at"}},
		{"API balance", []any{"providers", "openai", "usage", "api_credit_balances", 0}, []string{"pool", "name", "currency", "balance", "observed_at", "grants"}},
		{"API expiry", []any{"providers", "openai", "usage", "api_credit_balances", 0, "grants", 0}, []string{"remaining", "expires_at"}},
		{"pool", []any{"providers", "openai", "usage", "credit_pools", 0}, []string{"name", "organization", "claude_source", "plan", "monthly_credit", "currency", "balance", "month_spend", "month_resets_at", "expires_at", "expiring", "burn_per_day", "burn_days", "observed_at", "live", "observation_source", "signed_out", "extension_contact_at", "projected_spend_before_expiry", "projected_unused_at_expiry", "runs_out_at", "next_credit_at", "expired_since_check"}},
		{"account view", []any{"providers", "openai", "view"}, []string{"credit_grants", "credit_benefits", "api_credit_balances", "local_models"}},
		{"evaluated grant", []any{"providers", "openai", "view", "credit_grants", 0}, []string{"name", "label", "scope", "currency", "state", "used", "limit", "remaining", "utilization", "expires_at"}},
		{"expired grant", []any{"providers", "openai", "view", "credit_grants", 1}, []string{"last_observed_remaining"}},
		{"evaluated benefit", []any{"providers", "openai", "view", "credit_benefits", 0}, []string{"state", "text", "action_url"}},
		{"evaluated API balance", []any{"providers", "openai", "view", "api_credit_balances", 0}, []string{"state", "available", "text", "action_url"}},
		{"local account view", []any{"providers", "local", "view"}, []string{"local_model_status"}},
		{"local status", []any{"providers", "local", "view", "local_model_status"}, []string{"text", "remaining_quota_known", "reset_known", "latest_limit_model", "latest_limit_label", "latest_limit_at"}},
	}
	for _, tc := range cases {
		for _, field := range tc.fields {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				doc := statusCoverageDocument(t, encoded)
				object := coverageObject(t, doc, tc.path)
				if _, ok := object[field]; !ok {
					t.Fatalf("fixture did not emit %s", field)
				}
				object[field] = map[string]any{} // wrong for every scalar and array
				if field == "announced_reset" || field == "local_model_status" {
					object[field] = "not an object"
				}
				if err := schema.Validate(doc); err == nil {
					t.Fatal("schema accepted the wrong type")
				}
				object[field] = nil
				nullable := (tc.name == "announcement" && field == "windows") || (tc.name == "API balance" && field == "grants")
				if err := schema.Validate(doc); (err == nil) != nullable {
					t.Fatalf("null acceptance does not match the encoder: nullable=%v, err=%v", nullable, err)
				}
				// Removing any new optional property must still be valid.
				delete(object, field)
				if err := schema.Validate(doc); err != nil {
					t.Fatalf("optional field became required: %v", err)
				}
				object["future_optional_field"] = true
				if err := schema.Validate(doc); err != nil {
					t.Fatalf("additive extension rejected: %v", err)
				}
			})
		}
	}
}

// This checks the shared encoder, not credential discovery or the unexported
// CLI printOutput branch. A command-level no-provider regression is separate.
func TestEmptyStatusJSONMatchesPublishedSchema(t *testing.T) {
	encoded, err := (&cli.MultiProviderOutput{}).JSON(nil, time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	doc := statusCoverageDocument(t, encoded)
	if err := compileSchema(t, "status-v1.schema.json").Validate(doc); err != nil {
		t.Fatalf("empty status rejected: %v\n%s", err, encoded)
	}
	providers, ok := doc["providers"].(map[string]any)
	if !ok || len(providers) != 0 || doc["schema_version"] != float64(1) || doc["error"] != "no active providers" {
		t.Fatalf("empty status lost its v1 envelope or error: %s", encoded)
	}
}
