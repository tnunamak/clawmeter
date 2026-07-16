package provider

import "testing"

func TestGetMaturityUsesExplicitAuditClassification(t *testing.T) {
	tests := map[string]Maturity{
		"claude":     MaturityOrdinary,
		"openai":     MaturityOrdinary,
		"gemini":     MaturityOrdinary,
		"xai":        MaturityOrdinary,
		"kimi":       MaturityExperimental,
		"kimik2":     MaturityExperimental,
		"copilot":    MaturityExperimental,
		"openrouter": MaturityExperimental,
		"jetbrains":  MaturityExperimental,
		"synthetic":  MaturityExperimental,
		"zai":        MaturityExperimental,
	}
	for name, want := range tests {
		got := GetMaturity(name)
		if got.Level != want {
			t.Errorf("GetMaturity(%q).Level = %q, want %q", name, got.Level, want)
		}
		if got.LearnMore != "docs/provider-maturity.md" {
			t.Errorf("GetMaturity(%q).LearnMore = %q", name, got.LearnMore)
		}
	}
	if len(providerMaturityByName) != len(tests) {
		t.Fatalf("classification table has %d entries, want explicit coverage for %d current providers", len(providerMaturityByName), len(tests))
	}
}
