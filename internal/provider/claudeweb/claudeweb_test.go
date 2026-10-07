package claudeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/anthropic"
)

type testClaudeSource struct{ id, label, org string }

func (testClaudeSource) Name() string                     { return "claude" }
func (testClaudeSource) DisplayName() string              { return "Claude" }
func (testClaudeSource) Description() string              { return "test" }
func (testClaudeSource) DashboardURL() string             { return "" }
func (testClaudeSource) IsConfigured() bool               { return true }
func (p testClaudeSource) SourceID() string               { return p.id }
func (p testClaudeSource) SourceLabel() string            { return p.label }
func (testClaudeSource) SourceRevision() string           { return "" }
func (p testClaudeSource) ClaudeOrganizationUUID() string { return p.org }
func (testClaudeSource) FetchUsage(context.Context) (*provider.UsageData, error) {
	return &provider.UsageData{Provider: "claude"}, nil
}

func isolateUserCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
}

func writeSnapshot(t *testing.T, key, org string, observedAt time.Time, credits ...time.Time) {
	t.Helper()
	if err := claudeweb.WriteSummary(claudeweb.GrantSummary{Credits: credits, ObservedAt: observedAt, ResetCreditsTarget: key, MatchSalt: "salt", MatchHash: claudeweb.AccountHash("salt", org)}); err != nil {
		t.Fatal(err)
	}
}

func writeProfile(t *testing.T, path, org string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"organizationUuid": org}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBrowserCheckRoutesByOrganizationAcrossRealProfiles runs a real
// loopback check against two real Claude profiles and delivers the result
// the way the bookmark does.
func TestBrowserCheckRoutesByOrganizationAcrossRealProfiles(t *testing.T) {
	isolateUserCache(t)
	home, _ := os.UserHomeDir()
	writeProfile(t, filepath.Join(home, ".claude.json"), "org-default")
	odlDir := t.TempDir()
	writeProfile(t, filepath.Join(odlDir, ".claude.json"), "org-odl")
	native := config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}
	odl := config.SourceConfig{ID: "odl", Label: "ODL", Credential: config.CredentialRef{Kind: "config-dir", Ref: odlDir}}
	cfg := config.ProviderConfig{Sources: []config.SourceConfig{native, odl}}
	sources := []provider.Provider{anthropic.NewSource(cfg, native), anthropic.NewSource(cfg, odl)}

	session, err := StartCheck(sources)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:17343/challenge", nil)
	req.Header.Set("Origin", "https://claude.ai")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var challenge struct{ Nonce string }
	_ = json.NewDecoder(resp.Body).Decode(&challenge)
	resp.Body.Close()
	paused := false
	body, _ := json.Marshal(map[string]any{
		"nonce":   challenge.Nonce,
		"account": claudeweb.AccountHash(challenge.Nonce, "org-odl"),
		"grants": []map[string]any{{
			"resets_left": 1, "paused": &paused,
			"starts_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "ends_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		}},
	})
	post, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:17343/result", bytes.NewReader(body))
	post.Header.Set("Origin", "https://claude.ai")
	resp, err = client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct{ Message string }
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || reply.Message != "Found 1 reset for ODL." {
		t.Fatalf("result = %d %q", resp.StatusCode, reply.Message)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := session.Wait(ctx)
	if err != nil || summary.ResetCreditsTarget != "claude:odl" {
		t.Fatalf("Wait() = %+v, %v", summary, err)
	}
	if NewFor(sources[0]).IsConfigured() {
		t.Fatal("the Default source received the ODL result")
	}
	data, err := NewFor(sources[1]).FetchUsage(context.Background())
	if err != nil || data.ResetCredits.DisplayCount(time.Now()) != 1 {
		t.Fatalf("ODL FetchUsage() = %+v, %v", data, err)
	}
	presented := provider.PresentResetCreditSources(map[string]*provider.UsageData{
		"claude:odl":     {Provider: "claude", SourceID: "odl", Windows: []provider.UsageWindow{{Name: "5h", Utilization: 10}}},
		"claude_web:odl": data,
	})
	if got := presented["claude:odl"].ResetCredits.DisplayCount(time.Now()); got != 1 || len(presented) != 1 {
		t.Fatalf("presented = %#v, want one reset under claude:odl", presented)
	}
}

func TestSnapshotIsDroppedWhenTheSourceSignsInToAnotherOrganization(t *testing.T) {
	isolateUserCache(t)
	writeSnapshot(t, "claude:work", "org-a", time.Now(), time.Now().Add(24*time.Hour))
	before := NewFor(testClaudeSource{id: "work", label: "Work", org: "org-a"})
	if !before.IsConfigured() {
		t.Fatal("snapshot for the matching organization should be shown")
	}
	after := NewFor(testClaudeSource{id: "work", label: "Work", org: "org-b"})
	if after.IsConfigured() {
		t.Fatal("snapshot shown after the source switched organizations")
	}
	if _, err := after.FetchUsage(context.Background()); err == nil {
		t.Fatal("FetchUsage() returned another organization's snapshot")
	}
	if after.SourceRevision() == before.SourceRevision() {
		t.Fatal("cached snapshot would survive the organization change")
	}
	if NewFor(testClaudeSource{id: "work", label: "Work"}).IsConfigured() {
		t.Fatal("snapshot shown for a source with no recorded organization")
	}
}

func TestSnapshotStaysOnItsOwnSourceKey(t *testing.T) {
	isolateUserCache(t)
	writeSnapshot(t, "claude", "org-a", time.Now(), time.Now().Add(24*time.Hour))
	if NewFor(testClaudeSource{id: "odl", label: "ODL", org: "org-a"}).IsConfigured() {
		t.Fatal("Default snapshot attached to another source key")
	}
}

func TestFetchUsageExposesResetOnlySnapshot(t *testing.T) {
	isolateUserCache(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	expires := []time.Time{observedAt.Add(24 * time.Hour), observedAt.Add(48 * time.Hour)}
	writeSnapshot(t, "claude", "org-a", observedAt, expires...)
	p := NewFor(testClaudeSource{id: "default", label: "Default", org: "org-a"})
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.Provider != "claude_web" || data.SourceID != "default" || data.ResetCreditsTarget != "claude" || !data.FetchedAt.Equal(observedAt) {
		t.Fatalf("source identity/timestamp = %+v", data)
	}
	if data.Windows != nil || !data.ResetCredits.Snapshot || data.ResetCredits.DisplayCount(time.Now()) != 2 {
		t.Fatalf("data = %+v, want reset-only snapshot with two credits", data)
	}
	if earliest, ok := data.ResetCredits.EarliestExpiry(time.Now()); !ok || !earliest.Equal(expires[0]) {
		t.Fatalf("EarliestExpiry() = %s, %t", earliest, ok)
	}
}

func TestExpiredCreditsAreDroppedAndAllExpiredHidesTheSource(t *testing.T) {
	isolateUserCache(t)
	writeSnapshot(t, "claude", "org-a", time.Now(), time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	p := NewFor(testClaudeSource{id: "default", org: "org-a"})
	data, err := p.FetchUsage(context.Background())
	if err != nil || data.ResetCredits.DisplayCount(time.Now()) != 1 {
		t.Fatalf("FetchUsage() = %+v, %v; want only the unexpired credit", data, err)
	}
	writeSnapshot(t, "claude", "org-a", time.Now(), time.Now().Add(-time.Minute))
	if p.IsConfigured() {
		t.Fatal("IsConfigured() = true with every credit expired")
	}
}

func TestAccountsLabelSourcesAsTheTrayDoes(t *testing.T) {
	one := Accounts([]provider.Provider{testClaudeSource{id: "default", org: "org-a"}})
	if len(one) != 1 || one[0] != (claudeweb.Account{Key: "claude", Label: "Claude", OrgUUID: "org-a"}) {
		t.Fatalf("single source = %+v", one)
	}
	two := Accounts([]provider.Provider{testClaudeSource{id: "default", org: "org-a"}, testClaudeSource{id: "odl", label: "ODL", org: "org-b"}})
	if two[0].Label != "Default" || two[1].Label != "ODL" || two[1].Key != "claude:odl" {
		t.Fatalf("two sources = %+v", two)
	}
}
