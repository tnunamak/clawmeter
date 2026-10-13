package claudeweb

import (
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
