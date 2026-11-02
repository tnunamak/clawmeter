package cli

import (
	"encoding/json"
	"github.com/tnunamak/clawmeter/internal/provider"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResetExpiryScheduleAgreesAcrossSurfaces(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	r := &provider.UsageResetCredits{Snapshot: true, FetchedAt: now, Credits: []provider.UsageResetCredit{
		{Status: "available", ExpiresAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local)},
		{Status: "available", ExpiresAt: time.Date(2026, 11, 7, 17, 0, 0, 0, time.Local)},
		{Status: "available"},
	}}
	data := &provider.UsageData{Provider: "claude", FetchedAt: now, ResetCredits: r}
	v := (&MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", DisplayName: "Claude", Data: data}}}).buildView(now)
	for name, text := range map[string]string{"plain": v.plainLine(0), "table": strings.Join(v.colorTable(), "\n")} {
		for _, want := range append([]string{"Nov 6", "Nov 7", "1 expiry unknown"}, r.ExpirySchedule(now).Details(time.Local)...) {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q: %s", name, want, text)
			}
		}
	}
	acct := v.jsonAccount("claude")
	if acct.ResetCredits == nil || !reflect.DeepEqual(acct.ResetCredits.Schedule, r.ExpirySchedule(now)) {
		t.Fatalf("JSON schedule = %+v", acct.ResetCredits)
	}
	scheduleJSON, _ := json.Marshal(acct.ResetCredits.Schedule)
	if !strings.Contains(v.agentLine(), "expiry_schedule="+string(scheduleJSON)) {
		t.Fatal("agent lost evaluated schedule")
	}
	// All account usage failures retain the account-bound browser observation.
	for name, usage := range claudeUsageStates(now) {
		if usage == nil {
			usage = &provider.UsageData{Provider: "claude"}
		}
		usage.ResetCredits = r
		other := (&MultiProviderOutput{Providers: []ProviderFormatter{{Name: "claude", DisplayName: "Claude", Data: usage}}}).buildView(now)
		if !strings.Contains(other.plainLine(0), "Nov 7, 2026 at 17:00") {
			t.Fatalf("%s lost exact snapshot schedule", name)
		}
	}
}
