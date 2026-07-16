package jetbrains

import "testing"

func TestParseQuotaXMLRequiresExplicitLimitAndUsage(t *testing.T) {
	for _, input := range []string{
		`<application><component><option name="monthlyCreditsLimit" value="100"/></component></application>`,
		`<application><component><option name="monthlyCreditsUsed" value="0"/></component></application>`,
	} {
		if quota, err := parseQuotaXML([]byte(input)); err == nil || quota != nil {
			t.Fatalf("parseQuotaXML(%q) = %#v, %v; want incomplete data rejected", input, quota, err)
		}
	}
}

func TestParseQuotaXMLPreservesExplicitZeroAndUnknownReset(t *testing.T) {
	input := `<application><component><option name="monthlyCreditsLimit" value="100"/><option name="monthlyCreditsUsed" value="0"/></component></application>`
	quota, err := parseQuotaXML([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	data := (&Provider{}).transformQuota(quota)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("data = %#v, want explicit zero usage and unknown reset", data)
	}
}
