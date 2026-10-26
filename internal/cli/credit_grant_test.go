package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/tray"
)

func TestCloudCreditFactsAgreeAcrossFullDetailSurfaces(t *testing.T) {
	now := matrixNow
	used, limit := 7.125, 250.0
	for _, state := range []string{"available", "stale", "expired", "grant only", "login expired", "usage unavailable"} {
		t.Run(state, func(t *testing.T) {
			expiry := now.Add(time.Hour)
			if state == "expired" {
				expiry = now
			}
			data := &provider.UsageData{Provider: "claude", SourceID: "default", FetchedAt: now, Stale: state == "stale",
				Windows:      []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 10, ResetsAt: now.Add(time.Hour)}},
				CreditGrants: []provider.UsageCreditGrant{{Name: "cloud_sessions", DisplayName: "Cloud session credits", Scope: "cloud_sessions", Currency: "USD", Used: &used, Limit: &limit, ExpiresAt: expiry}},
			}
			if state == "grant only" {
				data.Windows = nil
			}
			if state == "login expired" {
				data.IsExpired = true
			}
			if state == "usage unavailable" {
				data.Error = "usage unavailable"
			}
			m := &MultiProviderOutput{Providers: []ProviderFormatter{
				{Name: "claude", DisplayName: "Claude", Family: "claude", SourceID: "default", SourceLabel: "Default", SourceCount: 2, Data: data},
				{Name: "claude:work", DisplayName: "Claude", Family: "claude", SourceID: "work", SourceLabel: "Work", SourceCount: 2, Data: &provider.UsageData{Provider: "claude", SourceID: "work", FetchedAt: now, Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 3, ResetsAt: now.Add(4 * time.Hour)}}}},
			}}
			v := m.buildView(now)
			acct := v.account("claude")
			grant := acct.CreditGrants[0]
			plain, table, agent := v.plainLine(0), strings.Join(v.colorTable(), "\n"), v.agentLine()
			if !strings.Contains(plain, grant.Summary()) || !strings.Contains(table, grant.Summary()) || strings.Contains(v.plainLine(1), "Cloud session credits") {
				t.Fatalf("human surfaces lost/misassigned a grant:\n%s\n%s", plain, table)
			}
			if !strings.Contains(agent, "credit_grants=[Claude · Default · Cloud session credits scope=cloud_sessions currency=USD state="+grant.State) || strings.Contains(agent, "Work · Cloud session credits") {
				t.Fatalf("agent grant facts = %s", agent)
			}
			if (state == "expired" || state == "stale") && (!strings.Contains(agent, "last_observed_remaining=") || strings.Contains(agent, " state="+grant.State+" used=7.125 limit=250 remaining=")) {
				t.Fatal("agent advertised an old balance as currently remaining")
			}
			for _, row := range tray.MenuRows(v.snap, 8)["claude"] {
				if strings.Contains(row, "Cloud") || strings.Contains(row, "Bonus") {
					t.Fatal("grant rendered as a quota bar")
				}
			}
			encoded, err := v.jsonDocument(nil, now)
			if err != nil {
				t.Fatal(err)
			}
			var doc JSONOutput
			if err := json.Unmarshal(encoded, &doc); err != nil {
				t.Fatal(err)
			}
			claude := doc.Providers["claude"]
			if len(claude.View.CreditGrants) != 1 || claude.View.CreditGrants[0].State != grant.State || len(claude.Usage.CreditGrants) != 1 {
				t.Fatalf("JSON omitted raw/effective grant: %s", encoded)
			}
			for _, source := range claude.Sources {
				if source.Source.ID == "work" && len(source.View.CreditGrants) != 0 {
					t.Fatal("JSON copied grant to another account")
				}
			}
		})
	}
}
