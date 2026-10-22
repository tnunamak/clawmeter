package claudeweb

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func isolateUserCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
}

var testAccounts = []Account{
	{Key: "claude", Label: "Default", OrgUUID: "org-default"},
	{Key: "claude:odl", Label: "ODL", OrgUUID: "org-odl"},
	{Key: "claude:spare", Label: "Spare"}, // no org recorded yet
}

func TestGrantInventoryCountsOnlyStartedUnpausedUnexpiredCredits(t *testing.T) {
	now := time.Now().UTC()
	paused, active := true, false
	credits, err := activeGrantExpiries([]grantPayload{
		{ResetsLeft: new(2), StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano), Paused: &active},
		{ResetsLeft: new(1), StartsAt: now.Add(time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(48 * time.Hour).Format(time.RFC3339Nano), Paused: &active},
		{ResetsLeft: new(3), StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(48 * time.Hour).Format(time.RFC3339Nano), Paused: &paused},
		{ResetsLeft: new(1), StartsAt: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Paused: &active},
	}, now)
	if err != nil || len(credits) != 2 {
		t.Fatalf("credits = %v, %v; want only the 2 active resets", credits, err)
	}
}

func TestGrantInventoryRejectsUnknownPauseOrSchedule(t *testing.T) {
	now := time.Now().UTC()
	if _, err := activeGrantExpiries([]grantPayload{{ResetsLeft: new(1), StartsAt: now.Format(time.RFC3339Nano), EndsAt: now.Add(time.Hour).Format(time.RFC3339Nano)}}, now); err == nil {
		t.Fatal("missing paused state accepted")
	}
	paused := false
	if _, err := activeGrantExpiries([]grantPayload{{ResetsLeft: new(1), EndsAt: now.Add(time.Hour).Format(time.RFC3339Nano), Paused: &paused}}, now); err == nil {
		t.Fatal("missing start time accepted")
	}
}

func TestGrantSummaryNextCreditDateRoundTrips(t *testing.T) {
	next := time.Date(2026, 10, 24, 5, 50, 6, 0, time.UTC)
	observed := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(GrantSummary{ObservedAt: observed, ResetCreditsTarget: "claude:odl", NextCreditAt: &next})
	if err != nil {
		t.Fatal(err)
	}
	var restored GrantSummary
	if err := json.Unmarshal(data, &restored); err != nil || restored.NextCreditAt == nil || !restored.NextCreditAt.Equal(next) {
		t.Fatalf("round trip = %+v, %v; json %s", restored, err, data)
	}
	data, err = json.Marshal(GrantSummary{ObservedAt: observed, ResetCreditsTarget: "claude:odl"})
	if err != nil || strings.Contains(string(data), "next_credit_at") {
		t.Fatalf("unknown next credit serialized: %s, %v", data, err)
	}
}
