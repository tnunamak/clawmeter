package cache

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestInvalidatingErrorRoundTripCannotResurrectUsage(t *testing.T) {
	entry := Entry{ProviderData: map[string]*provider.UsageData{
		"xai": {
			Provider:              "xai",
			Error:                 "Grok usage percentage unavailable",
			InvalidatesPriorUsage: true,
		},
	}}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Entry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.ProviderData["xai"]
	if got == nil || got.Error == "" || got.HasPresentableUsage() {
		t.Fatalf("round-tripped invalidation = %#v, want error-only data", got)
	}
}

func TestIsValid(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
		want  bool
	}{
		{"fresh", Entry{FetchedAt: time.Now()}, true},
		{"30s old", Entry{FetchedAt: time.Now().Add(-30 * time.Second)}, true},
		{"61s old (stale)", Entry{FetchedAt: time.Now().Add(-61 * time.Second)}, false},
		{"5m old (stale)", Entry{FetchedAt: time.Now().Add(-5 * time.Minute)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.entry.IsValid(); got != c.want {
				t.Fatalf("IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCovers(t *testing.T) {
	entry := Entry{
		ProviderData: map[string]*provider.UsageData{
			"openai": {Provider: "openai"},
			"claude": {Provider: "claude"},
		},
	}

	cases := []struct {
		name string
		want []string
		out  bool
	}{
		{"empty want", nil, true},
		{"all present", []string{"openai", "claude"}, true},
		{"subset present", []string{"openai"}, true},
		{"missing one", []string{"openai", "gemini"}, false},
		{"all missing", []string{"gemini", "kimi"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entry.Covers(c.want); got != c.out {
				t.Fatalf("Covers(%v) = %v, want %v", c.want, got, c.out)
			}
		})
	}
}

// Covers must treat an entry whose value is nil (provider was attempted but
// returned no data) as "covered" — otherwise the cache would never serve a
// provider that has yet to return a successful result.
func TestCoversTreatsNilEntryAsCovered(t *testing.T) {
	entry := Entry{
		ProviderData: map[string]*provider.UsageData{
			"openai": nil,
		},
	}
	if !entry.Covers([]string{"openai"}) {
		t.Fatal("Covers should return true when openai key exists, even if value is nil")
	}
}

func TestHasStaleData(t *testing.T) {
	entry := Entry{
		ProviderData: map[string]*provider.UsageData{
			"openai": {Provider: "openai"},
			"claude": {Provider: "claude", Stale: true},
		},
	}

	if !entry.HasStaleData([]string{"claude"}) {
		t.Fatal("HasStaleData(claude) = false, want true")
	}
	if entry.HasStaleData([]string{"openai"}) {
		t.Fatal("HasStaleData(openai) = true, want false")
	}
	if entry.HasStaleData([]string{"gemini"}) {
		t.Fatal("HasStaleData(missing) = true, want false")
	}
}
