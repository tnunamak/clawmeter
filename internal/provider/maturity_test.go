package provider

import "testing"

func TestGetMaturityUsesExplicitAuditClassification(t *testing.T) {
	tests := map[string]bool{
		"claude": false, "openai": false, "gemini": false, "xai": true,
		"kimi": true, "kimik2": true, "copilot": true, "openrouter": true,
		"jetbrains": true, "synthetic": true, "zai": true,
	}
	for name, want := range tests {
		got := GetMaturity(name)
		if got.Experimental != want {
			t.Errorf("GetMaturity(%q).Experimental = %t, want %t", name, got.Experimental, want)
		}
		if want && got.LearnMore != providerMaturityLearnMore {
			t.Errorf("GetMaturity(%q).LearnMore = %q", name, got.LearnMore)
		}
		if !want && got.LearnMore != "" {
			t.Errorf("GetMaturity(%q).LearnMore = %q for a non-experimental provider", name, got.LearnMore)
		}
	}
	if len(experimentalProviderByName) != len(tests) {
		t.Fatalf("classification table has %d entries, want explicit coverage for %d current providers", len(experimentalProviderByName), len(tests))
	}
	if !GetMaturity("future-provider").Experimental {
		t.Fatal("unknown providers must default to experimental")
	}
}
