package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// A configured source with no reading yet is named in view.accounts, so it
// also has a providers entry that carries only its view, with reading state
// unavailable. It is not mistaken for zero use.
func TestSingleSourceWithNoReadingHasAViewObject(t *testing.T) {
	m := &MultiProviderOutput{Providers: []ProviderFormatter{fmtr("claude", "Claude", "claude", nil)}}
	doc, err := m.JSON(nil, matrixNow)
	if err != nil {
		t.Fatal(err)
	}
	var out JSONOutput
	if err := json.Unmarshal(doc, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.View.Accounts) != 1 || out.View.Accounts[0] != "claude" {
		t.Fatalf("view.accounts = %v, want [claude]", out.View.Accounts)
	}
	p := out.Providers["claude"]
	if p == nil || p.View == nil || p.View.Reading.State != "unavailable" || len(p.View.Windows) != 0 || p.Usage != nil {
		t.Fatalf("providers[claude] = %+v, want only a view with reading unavailable", p)
	}
}

// Each account is labelled by the view's rule: the display name, plus the
// source label only when the family has more than one configured source.
func TestLoneNamedSourceIsLabelledByTheViewRule(t *testing.T) {
	now := matrixNow
	data := func() *provider.UsageData {
		return &provider.UsageData{Provider: "claude", SourceID: "odl", FetchedAt: now,
			Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 10, ResetsAt: now.Add(4 * time.Hour)}}}
	}
	lone := ProviderFormatter{Name: "claude:odl", DisplayName: "Claude", SourceLabel: "ODL", SourceID: "odl", Family: "claude", SourceCount: 1, Data: data()}
	if got := (&MultiProviderOutput{Providers: []ProviderFormatter{lone}}).buildView(now).plainLine(0); !strings.HasPrefix(got, "Claude: ") {
		t.Fatalf("lone named source plain = %q, want the view's label Claude", got)
	}
	two := lone
	two.SourceCount = 2
	if got := (&MultiProviderOutput{Providers: []ProviderFormatter{two}}).buildView(now).plainLine(0); !strings.HasPrefix(got, "Claude · ODL: ") {
		t.Fatalf("second of two sources plain = %q, want Claude · ODL", got)
	}
}

// The agent line names each account by the same label as the plain line.
func TestAgentAndPlainShareOneAccountLabel(t *testing.T) {
	now := matrixNow
	pf := ProviderFormatter{Name: "claude:odl", DisplayName: "Claude", SourceLabel: "ODL", SourceID: "odl", Family: "claude", SourceCount: 1,
		Data: &provider.UsageData{Provider: "claude", SourceID: "odl", FetchedAt: now,
			Windows: []provider.UsageWindow{{Name: "5h", Length: 5 * time.Hour, Utilization: 10, ResetsAt: now.Add(4 * time.Hour)}}}}
	v := (&MultiProviderOutput{Providers: []ProviderFormatter{pf}}).buildView(now)
	if plain, agent := v.plainLine(0), v.agentLine(); !strings.HasPrefix(plain, "Claude: ") || !strings.HasPrefix(agent, "Quota: worst=Claude 5h") {
		t.Fatalf("plain = %q, agent = %q; want both to say Claude", plain, agent)
	}
}
