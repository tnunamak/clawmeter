package provider

import "testing"

func TestLinkedCreditPoolsMoveUnderTheirClaudeSource(t *testing.T) {
	linked := UsageCreditPool{Name: "Family's Individual Organization", Organization: "Family's Individual Organization", Source: "claude", Balance: 19988}
	gone := UsageCreditPool{Name: "Lost", Organization: "Lost", Source: "claude:missing", Balance: 5}
	plain := UsageCreditPool{Name: "Dondo Org", Organization: "Dondo Org", Balance: 100}
	results := map[string]*UsageData{
		"claude":     {Provider: "claude", SourceID: "default", Windows: []UsageWindow{{Name: "weekly"}}},
		"claude_api": {Provider: ClaudeAPISourceName, CreditPools: []UsageCreditPool{linked, gone, plain}},
	}
	presented := PresentResetCreditSources(results)
	pools := presented["claude"].CreditPools
	if len(pools) != 1 || pools[0].Name != LinkedCreditPoolName || pools[0].Organization != "Family's Individual Organization" || len(presented["claude"].Windows) != 1 {
		t.Fatalf("claude row = %+v", presented["claude"])
	}
	rest := presented["claude_api"].CreditPools
	if len(rest) != 2 || rest[0].Name != "Lost" || rest[1].Name != "Dondo Org" {
		t.Fatalf("claude_api row = %+v", rest)
	}
	if len(results["claude"].CreditPools) != 0 || len(results["claude_api"].CreditPools) != 3 {
		t.Fatal("the backing results were modified")
	}

	only := map[string]*UsageData{
		"claude":     {Provider: "claude", SourceID: "default"},
		"claude_api": {Provider: ClaudeAPISourceName, CreditPools: []UsageCreditPool{linked}},
	}
	if presented := PresentResetCreditSources(only); presented["claude_api"] != nil || len(presented["claude"].CreditPools) != 1 {
		t.Fatalf("an emptied Claude API row must disappear: %+v", presented)
	}
}

func TestLinkedCreditPoolDoesNotMaskAFailedUsageReading(t *testing.T) {
	results := map[string]*UsageData{
		"claude":     {Provider: "claude", SourceID: "default", Error: "usage request failed (500)"},
		"claude_api": {Provider: ClaudeAPISourceName, CreditPools: []UsageCreditPool{{Name: "Org", Source: "claude", Balance: 1}}},
	}
	row := PresentResetCreditSources(results)["claude"]
	if len(row.CreditPools) != 1 || row.HasPresentableUsage() || !ShouldShowInPrimaryUI(row, false, false) {
		t.Fatalf("row = %+v, presentable = %v; want the pool shown on a visible row that stays failed", row, row.HasPresentableUsage())
	}
	if !(&UsageData{Provider: ClaudeAPISourceName, CreditPools: []UsageCreditPool{{Name: "Org"}}}).HasPresentableUsage() {
		t.Fatal("a healthy Claude API row must stay presentable")
	}
}
