package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyCloudCreditCacheKeepsOnlyObservedFacts(t *testing.T) {
	for _, family := range []string{"claude", "other"} {
		var data UsageData
		err := json.Unmarshal([]byte(`{"provider":"`+family+`","windows":[{"name":"5h","utilization":10},{"name":"bonus","display_name":"Bonus","utilization":2.85,"resets_at":"2026-11-05T07:59:00Z"}]}`), &data)
		if err != nil {
			t.Fatal(err)
		}
		if family == "other" {
			if len(data.CreditGrants) != 0 || len(data.Windows) != 2 {
				t.Fatal("changed another provider's bonus quota")
			}
			continue
		}
		if len(data.Windows) != 1 || len(data.CreditGrants) != 1 {
			t.Fatalf("migration = %+v", data)
		}
		grant := data.CreditGrants[0]
		if grant.Scope != "cloud_sessions" || grant.Used != nil || grant.Limit != nil || grant.Utilization == nil || *grant.Utilization != 2.85 || grant.ExpiresAt.IsZero() {
			t.Fatalf("invented or lost grant facts: %+v", grant)
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"name":"bonus"`) {
			t.Fatal("legacy resetting quota survived migration")
		}
		var again UsageData
		if err := json.Unmarshal(encoded, &again); err != nil || len(again.CreditGrants) != 1 {
			t.Fatalf("migration is not stable: %v %+v", err, again)
		}
	}
}

func TestCreditGrantCloneDoesNotAliasObservation(t *testing.T) {
	used, limit := 3.0, 250.0
	data := &UsageData{CreditGrants: []UsageCreditGrant{{Name: "cloud_sessions", Used: &used, Limit: &limit}}}
	copy := data.Clone()
	*copy.CreditGrants[0].Used = 90
	copy.CreditGrants[0].Name = "changed"
	if *data.CreditGrants[0].Used != 3 || data.CreditGrants[0].Name != "cloud_sessions" {
		t.Fatal("grant clone mutated its source")
	}
	if !data.HasPresentableUsage() || data.HasUsageWindows() {
		t.Fatal("a grant must be visible without establishing a quota window")
	}
}
