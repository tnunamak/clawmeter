package claudeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/anthropic"
)

// deliverBrowserResult plays the bookmark: fetch the one-time nonce, then post
// one usable grant, exactly as the browser does.
func deliverBrowserResult(t *testing.T, expires time.Time) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:17343/challenge", nil)
	req.Header.Set("Origin", "https://claude.ai")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var challenge struct{ Nonce string }
	err = json.NewDecoder(resp.Body).Decode(&challenge)
	resp.Body.Close()
	if err != nil || challenge.Nonce == "" {
		t.Fatalf("challenge = %+v, %v", challenge, err)
	}
	stamp := expires.UTC().Format(time.RFC3339Nano)
	body, _ := json.Marshal(map[string]any{"nonce": challenge.Nonce, "count": 1, "earliest": stamp, "credits": []string{stamp}})
	post, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:17343/result", bytes.NewReader(body))
	post.Header.Set("Origin", "https://claude.ai")
	resp, err = client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("result status = %d", resp.StatusCode)
	}
}

func TestBrowserCheckIsPresentedUnderSourceForNativeAndExplicitProfiles(t *testing.T) {
	native := config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}
	explicit := config.SourceConfig{ID: "odl", Label: "ODL", Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}}
	cfg := config.ProviderConfig{Sources: []config.SourceConfig{native, explicit}}
	targets := map[string]*anthropic.Provider{
		"claude":     anthropic.NewSource(cfg, native), // pinned native Default
		"claude:odl": anthropic.NewSource(cfg, explicit),
	}
	for key, target := range targets {
		t.Run(key, func(t *testing.T) {
			isolateUserCache(t)
			session, err := StartCheck(target)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			deliverBrowserResult(t, time.Now().Add(24*time.Hour))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := session.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := NewFor(target).FetchUsage(context.Background())
			if err != nil {
				t.Fatalf("a successful browser check was rejected: %v", err)
			}
			presented := provider.PresentResetCreditSources(map[string]*provider.UsageData{
				key: {Provider: "claude", SourceID: provider.SourceID(target), Windows: []provider.UsageWindow{{Name: "5h", Utilization: 10}}},
				"claude_web" + map[bool]string{true: "", false: ":odl"}[key == "claude"]: data,
			})
			if got := presented[key].ResetCredits.DisplayCount(time.Now()); got != 1 || len(presented) != 1 {
				t.Fatalf("presented = %#v, want one grant under %s", presented, key)
			}
		})
	}
}

func TestFetchUsageExposesIndependentWebSourceResetCredits(t *testing.T) {
	isolateUserCache(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	expires := []time.Time{observedAt.Add(24 * time.Hour), observedAt.Add(48 * time.Hour)}
	if err := claudeweb.WriteSummaryFor("claude", claudeweb.GrantSummary{Credits: expires, ObservedAt: observedAt}); err != nil {
		t.Fatal(err)
	}

	p := newDefault()
	if !p.IsConfigured() {
		t.Fatal("IsConfigured() = false, want a saved active browser observation")
	}
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.Provider != "claude_web" || data.SourceID != "default" || data.SourceLabel != "Default" || !data.FetchedAt.Equal(observedAt) {
		t.Fatalf("source identity/timestamp = %+v", data)
	}
	if data.Windows != nil || data.ResetCredits == nil || !data.ResetCredits.Snapshot || data.ResetCredits.DisplayCount(time.Now()) != 2 {
		t.Fatalf("data = %+v, want reset-only snapshot with two credits", data)
	}
	if earliest, ok := data.ResetCredits.EarliestExpiry(time.Now()); !ok || !earliest.Equal(expires[0]) {
		t.Fatalf("EarliestExpiry() = %s, %t; want %s", earliest, ok, expires[0])
	}
	if !data.HasPresentableUsage() || provider.SourceKey(p) != "claude_web" {
		t.Fatalf("reset-only source should be presentable: presentable=%v count=%d key=%q", data.HasPresentableUsage(), data.ResetCredits.DisplayCount(time.Now()), provider.SourceKey(p))
	}
}

func TestExpiredBrowserObservationIsNotConfigured(t *testing.T) {
	isolateUserCache(t)
	if err := claudeweb.WriteSummaryFor("claude", claudeweb.GrantSummary{
		Credits: []time.Time{time.Now().Add(-time.Minute)}, ObservedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if newDefault().IsConfigured() {
		t.Fatal("IsConfigured() = true, want false when all observed credits expired")
	}
}

func TestFetchUsageCarriesExplicitClaudeAccountAssociation(t *testing.T) {
	isolateUserCache(t)
	expiresAt := time.Now().Add(24 * time.Hour)
	if err := claudeweb.WriteSummaryFor("claude:odl", claudeweb.GrantSummary{
		Credits: []time.Time{expiresAt}, ObservedAt: time.Now(), SourceRevision: "credential-revision-1",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := NewFor(testClaudeSource{id: "odl", label: "ODL", revision: "credential-revision-1"}).FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.ResetCreditsTarget != "claude:odl" {
		t.Fatalf("reset target = %q, want selected Claude source", data.ResetCreditsTarget)
	}
}

func TestSnapshotDoesNotFollowChangedClaudeSource(t *testing.T) {
	isolateUserCache(t)
	if err := claudeweb.WriteSummaryFor("claude:work", claudeweb.GrantSummary{
		Credits: []time.Time{time.Now().Add(24 * time.Hour)}, ObservedAt: time.Now(), SourceRevision: "credential-revision-1",
	}); err != nil {
		t.Fatal(err)
	}

	oldSource := NewFor(testClaudeSource{id: "work", label: "Work", revision: "credential-revision-1"})
	if !oldSource.IsConfigured() {
		t.Fatal("snapshot for matching source revision should be configured")
	}
	newSource := NewFor(testClaudeSource{id: "work", label: "Work", revision: "credential-revision-2"})
	if newSource.IsConfigured() {
		t.Fatal("snapshot for old credential revision must not appear configured for replacement credentials")
	}
	if _, err := newSource.FetchUsage(context.Background()); err == nil {
		t.Fatal("FetchUsage() accepted reset snapshot from replaced credential source")
	}
	if got := newSource.SourceRevision(); got == oldSource.SourceRevision() {
		t.Fatalf("supplemental source revision did not follow target revision: %q", got)
	}
}

func TestDetailedInventoryDropsCreditsAfterTheirExpiry(t *testing.T) {
	isolateUserCache(t)
	if err := claudeweb.WriteSummaryFor("claude", claudeweb.GrantSummary{
		Credits: []time.Time{time.Now().Add(-time.Minute), time.Now().Add(time.Hour)}, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	data, err := newDefault().FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := data.ResetCredits.DisplayCount(time.Now()); got != 1 {
		t.Fatalf("DisplayCount() = %d, want only the unexpired credit", got)
	}
}

func newDefault() *Provider {
	return NewFor(testClaudeSource{id: "default", label: "Default"})
}

type testClaudeSource struct{ id, label, revision string }

func (testClaudeSource) Name() string                          { return "claude" }
func (testClaudeSource) DisplayName() string                   { return "Claude" }
func (testClaudeSource) Description() string                   { return "test" }
func (testClaudeSource) DashboardURL() string                  { return "" }
func (testClaudeSource) IsConfigured() bool                    { return true }
func (p testClaudeSource) SourceID() string                    { return p.id }
func (p testClaudeSource) SourceLabel() string                 { return p.label }
func (p testClaudeSource) SourceRevision() string              { return p.revision }
func (p testClaudeSource) ResetSnapshotSourceRevision() string { return p.revision }
func (testClaudeSource) FetchUsage(context.Context) (*provider.UsageData, error) {
	return &provider.UsageData{Provider: "claude"}, nil
}

func isolateUserCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
}

func TestNativeDefaultSnapshotHasNoProfileIdentityAndStaysOnItsOwnKey(t *testing.T) {
	isolateUserCache(t)
	expiry := time.Now().Add(24 * time.Hour)
	if err := claudeweb.WriteSummaryFor("claude", claudeweb.GrantSummary{Credits: []time.Time{expiry}, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Default has no stable profile identity: it matches with an empty revision.
	if !newDefault().IsConfigured() {
		t.Fatal("Default snapshot should match the Default source")
	}
	// A different source key never reads the Default snapshot.
	if NewFor(testClaudeSource{id: "odl", label: "ODL", revision: ""}).IsConfigured() {
		t.Fatal("Default snapshot attached to a different source key")
	}
	// A Default source that gains a profile identity does not inherit an
	// identity-less snapshot.
	if NewFor(testClaudeSource{id: "default", label: "Default", revision: "profile-1"}).IsConfigured() {
		t.Fatal("identity-less snapshot attached to a source with a different profile identity")
	}
}
