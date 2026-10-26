package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCloudCreditsAreAnExpiringGrantNotAQuota(t *testing.T) {
	p := newTestProvider(t, `{"five_hour":{"utilization":10,"resets_at":"2026-10-26T17:30:00Z"},"iguana_necktie":{"utilization":2.85,"used_dollars":7.125,"limit_dollars":250,"resets_at":"2026-11-05T07:59:00Z"}}`, 200, "")
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range data.Windows {
		if window.Name == "bonus" {
			t.Fatal("cloud-session credit was presented as a resetting quota")
		}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Grants []struct {
			Name, Currency string
			Used, Limit    float64
			ExpiresAt      string `json:"expires_at"`
		} `json:"credit_grants"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Grants) != 1 || got.Grants[0].Currency != "USD" || got.Grants[0].Used != 7.125 || got.Grants[0].Limit != 250 || got.Grants[0].ExpiresAt != "2026-11-05T07:59:00Z" {
		t.Fatalf("cloud credit facts = %s", encoded)
	}
}

func TestCloudCreditsMissingAndPartialPayloads(t *testing.T) {
	for _, tc := range []struct {
		name, grant string
		want        int
	}{
		{"absent grant", "null", 0},
		{"empty grant", "{}", 0},
		{"invalid amounts", `{"used_dollars":-1,"limit_dollars":-2}`, 0},
		{"percentage only", `{"utilization":2.85,"resets_at":"2026-11-05T07:59:00Z"}`, 1},
		{"unused grant", `{"used_dollars":0,"limit_dollars":250}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProvider(t, `{"iguana_necktie":`+tc.grant+`}`, 200, "")
			data, err := p.FetchUsage(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(data.CreditGrants) != tc.want || len(data.Windows) != 0 {
				t.Fatalf("partial grant = %+v", data)
			}
			if tc.want > 0 && data.Error != "" {
				t.Fatalf("grant-only reading rejected: %s", data.Error)
			}
			if tc.name == "percentage only" && (data.CreditGrants[0].Used != nil || data.CreditGrants[0].Limit != nil) {
				t.Fatal("derived dollars from a percentage")
			}
		})
	}
}

func TestCloudCreditsSurviveUnavailablePrimaryQuota(t *testing.T) {
	p := newTestProvider(t, `{"five_hour":{"utilization":0},"seven_day":{"utilization":0},"seven_day_sonnet":{"utilization":0},"iguana_necktie":{"used_dollars":0,"limit_dollars":250}}`, 200, "")
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.CreditGrants) != 1 || !strings.Contains(data.Error, "usage unavailable") {
		t.Fatalf("supplemental grant lost: data=%+v error=%v", data, err)
	}
}
