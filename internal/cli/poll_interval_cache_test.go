package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// The cache constructor also consults the wall clock for its independent
// two-minute stale marker. Keep the observation in the future relative to
// the test runner, then evaluate explicitly after it: these tests isolate
// the configured reading-age contract, not that wall-clock cache marker.
func pollIntervalCachedJSON(t *testing.T, pollSeconds int, age time.Duration, readingError string, expired bool) map[string]any {
	t.Helper()
	observed := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	data := &provider.UsageData{
		Provider: "claude", FetchedAt: observed, Error: readingError, IsExpired: expired,
		Windows: []provider.UsageWindow{{
			Name: "5h", Length: 5 * time.Hour, Utilization: 80,
			ResetsAt: observed.Add(3 * time.Hour),
		}},
	}
	registry := provider.NewRegistry()
	if err := registry.Register(cliStubProvider{name: "claude"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.PollInterval = pollSeconds
	entry := &cache.Entry{
		FetchedAt:    observed,
		ProviderData: map[string]*provider.UsageData{"claude": data},
	}
	output := buildOutputFromCache(registry, cfg, entry)
	if output.PollInterval != pollSeconds {
		t.Fatalf("cache output poll interval = %d, want %d", output.PollInterval, pollSeconds)
	}
	raw, err := output.JSON(entry, observed.Add(age))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return jpath(t, doc, "providers", "claude").(map[string]any)
}

func TestCachedJSONUsesConfiguredPollInterval(t *testing.T) {
	for _, tc := range []struct {
		name  string
		poll  int
		age   time.Duration
		fresh bool
	}{
		{"configured_15_minutes", 600, 15 * time.Minute, true},
		{"configured_exact_limit", 600, 20 * time.Minute, true},
		{"configured_past_limit", 600, 20*time.Minute + time.Nanosecond, false},
		{"configured_21_minutes", 600, 21 * time.Minute, false},
		{"default_15_minutes", 300, 15 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := pollIntervalCachedJSON(t, tc.poll, tc.age, "", false)
			state := jpath(t, p, "view", "reading", "state")
			if !tc.fresh {
				if state != "stale" {
					t.Fatalf("reading state = %v, want stale", state)
				}
				assertCachedJSONWithholdsForecast(t, p)
				return
			}
			if state != "fresh" {
				t.Fatalf("reading state = %v, want fresh", state)
			}
			legacy := jpath(t, p, "forecast", "windows", "5h").(map[string]any)
			if legacy["projected_pct"] != float64(200) || legacy["indicator"] == "stale" || legacy["indicator"] == "unknown" {
				t.Fatalf("fresh legacy forecast = %#v, want 200%% projection", legacy)
			}
			f := jpath(t, p, "view", "windows", 0, "forecast").(map[string]any)
			if f["state"] != "known" || f["pct"] != float64(200) || f["basis"] != "average_since_reset" {
				t.Fatalf("fresh view forecast = %#v", f)
			}
			e := jpath(t, p, "view", "windows", 0, "exhaustion").(map[string]any)
			wantSeconds := float64((30*time.Minute - tc.age) / time.Second)
			if e["state"] != "runs_out" || e["runs_out_in_seconds"] != wantSeconds {
				t.Fatalf("fresh exhaustion = %#v, want runs_out in %v seconds", e, wantSeconds)
			}
		})
	}
}

func TestCachedErrorAndExpiryWithholdJSONForecasts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     string
		expired bool
	}{
		{"error", "test provider unavailable", false},
		{"expired", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep the observation within the age allowance so the error or
			// credential expiry itself must suppress an otherwise known forecast.
			p := pollIntervalCachedJSON(t, 600, time.Minute, tc.err, tc.expired)
			if got := jpath(t, p, "view", "reading", "state"); got != tc.name {
				t.Fatalf("reading state = %v, want %s", got, tc.name)
			}
			assertCachedJSONWithholdsForecast(t, p)
		})
	}
}

func assertCachedJSONWithholdsForecast(t *testing.T, p map[string]any) {
	t.Helper()
	// The legacy contract retains a zero placeholder; it is not a forecast
	// unless the indicator is a percentage. Do not require removal of the object.
	legacy := jpath(t, p, "forecast", "windows", "5h").(map[string]any)
	if legacy["indicator"] != "stale" || legacy["projected_pct"] != float64(0) {
		t.Errorf("legacy published an unusable forecast: %#v", legacy)
	}
	for key := range legacy {
		if strings.Contains(key, "runs_out") || key == "early_by_seconds" || key == "out_since" {
			t.Errorf("legacy published run-out field %q: %#v", key, legacy)
		}
	}
	f := jpath(t, p, "view", "windows", 0, "forecast").(map[string]any)
	if f["state"] != "stale" || hasKey(f, "pct") || hasKey(f, "basis") {
		t.Errorf("view published an unusable forecast: %#v", f)
	}
	e := jpath(t, p, "view", "windows", 0, "exhaustion").(map[string]any)
	if e["state"] != "unknown" || hasKey(e, "runs_out_in_seconds") || hasKey(e, "early_by_seconds") || hasKey(e, "out_since") {
		t.Errorf("view published unusable run-out: %#v", e)
	}
}
