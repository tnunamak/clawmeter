package synthetic

import (
	"encoding/json"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestParseQuotasPreservesKnownSlotsAndZero(t *testing.T) {
	input := json.RawMessage(`{"rollingFiveHourLimit":{"limit":100,"used":0},"weeklyTokenLimit":{"limit":200,"used":50,"resetAt":"not-a-timestamp"},"search":{"hourly":{"limit":10,"used":5,"resetAt":"2026-07-16T18:00:00Z"}}}`)
	data, err := NewParseProvider().parseQuotas(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 3 {
		t.Fatalf("got %d windows, want 3", len(data.Windows))
	}
	if data.Windows[0].Utilization != 0 {
		t.Fatalf("zero lane utilization = %v", data.Windows[0].Utilization)
	}
	if !data.Windows[0].ResetsAt.IsZero() || !data.Windows[1].ResetsAt.IsZero() {
		t.Fatal("missing or malformed reset was invented")
	}
	if data.Windows[2].Name != "Search hourly" || data.Windows[2].Utilization != 50 {
		t.Fatalf("search lane = %+v", data.Windows[2])
	}
}

func TestParseQuotasUsesDocumentedSubscriptionAndRejectsUnknownShape(t *testing.T) {
	data, err := NewParseProvider().parseQuotas(json.RawMessage(`{"subscription":{"limit":135,"requests":0}}`))
	if err != nil || len(data.Windows) != 1 {
		t.Fatalf("subscription: data=%+v err=%v", data, err)
	}
	if data.Windows[0].Utilization != 0 || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("subscription window = %+v", data.Windows[0])
	}
	unknown, err := NewParseProvider().parseQuotas(json.RawMessage(`{"balance":100,"total":200}`))
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Error == "" || len(unknown.Windows) != 0 {
		t.Fatalf("unknown shape = %+v", unknown)
	}
}

func TestParseQuotasMalformedJSON(t *testing.T) {
	if _, err := NewParseProvider().parseQuotas(json.RawMessage(`{"subscription":`)); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestParseQuotasConflictingValuesPreferExplicitPercent(t *testing.T) {
	data, err := NewParseProvider().parseQuotas(json.RawMessage(`{"subscription":{"percentUsed":25,"limit":100,"used":90}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := data.Windows[0].Utilization; got != 25 {
		t.Fatalf("utilization = %v, want 25", got)
	}
}

// NewParseProvider keeps parser tests independent of credentials and transport.
func NewParseProvider() *Provider { return New(config.ProviderConfig{}) }
