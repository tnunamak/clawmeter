package cache

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestInvalidatingErrorRoundTripCannotResurrectUsage(t *testing.T) {
	entry := Entry{ProviderData: map[string]*provider.UsageData{
		"xai": {
			Provider:              "xai",
			Error:                 "Grok usage percentage unavailable",
			InvalidatesPriorUsage: true,
		},
	}}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Entry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.ProviderData["xai"]
	if got == nil || got.Error == "" || got.HasPresentableUsage() {
		t.Fatalf("round-tripped invalidation = %#v, want error-only data", got)
	}
}

func TestIsValid(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
		want  bool
	}{
		{"fresh", Entry{FetchedAt: time.Now()}, true},
		{"30s old", Entry{FetchedAt: time.Now().Add(-30 * time.Second)}, true},
		{"61s old (stale)", Entry{FetchedAt: time.Now().Add(-61 * time.Second)}, false},
		{"5m old (stale)", Entry{FetchedAt: time.Now().Add(-5 * time.Minute)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.entry.IsValid(); got != c.want {
				t.Fatalf("IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCovers(t *testing.T) {
	entry := Entry{
		ProviderData: map[string]*provider.UsageData{
			"openai": {Provider: "openai"},
			"claude": {Provider: "claude"},
		},
	}

	cases := []struct {
		name string
		want []string
		out  bool
	}{
		{"empty want", nil, true},
		{"all present", []string{"openai", "claude"}, true},
		{"subset present", []string{"openai"}, true},
		{"missing one", []string{"openai", "gemini"}, false},
		{"all missing", []string{"gemini", "kimi"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entry.Covers(c.want); got != c.out {
				t.Fatalf("Covers(%v) = %v, want %v", c.want, got, c.out)
			}
		})
	}
}

// Covers must treat an entry whose value is nil (provider was attempted but
// returned no data) as "covered" — otherwise the cache would never serve a
// provider that has yet to return a successful result.
func TestCoversTreatsNilEntryAsCovered(t *testing.T) {
	entry := Entry{
		ProviderData: map[string]*provider.UsageData{
			"openai": nil,
		},
	}
	if !entry.Covers([]string{"openai"}) {
		t.Fatal("Covers should return true when openai key exists, even if value is nil")
	}
}

func TestSourceEntriesRemainIndependent(t *testing.T) {
	entry := Entry{ProviderData: map[string]*provider.UsageData{
		"claude:personal": {Provider: "claude", SourceID: "personal"},
		"claude:work":     {Provider: "claude", SourceID: "work", Stale: true},
	}}
	if !entry.Covers([]string{"claude:personal", "claude:work"}) {
		t.Fatal("source cache entries should both be covered")
	}
	if entry.HasStaleData([]string{"claude:personal"}) {
		t.Fatal("work stale state leaked into personal")
	}
	if !entry.HasStaleData([]string{"claude:work"}) {
		t.Fatal("work stale state was lost")
	}
}

func TestSourceRevisionInvalidatesOnlyChangedSource(t *testing.T) {
	entry := Entry{ProviderData: map[string]*provider.UsageData{"claude:a": {}, "claude:b": {}}, SourceRevisions: map[string]string{"claude:a": "old", "claude:b": "same"}}
	if entry.CoversCurrent([]string{"claude:a", "claude:b"}, map[string]string{"claude:a": "new", "claude:b": "same"}) {
		t.Fatal("changed source should invalidate cache coverage")
	}
	if !entry.CoversCurrent([]string{"claude:b"}, map[string]string{"claude:b": "same"}) {
		t.Fatal("unchanged source should remain covered")
	}
	if (&Entry{ProviderData: map[string]*provider.UsageData{"claude:b": {}}}).CoversCurrent(
		[]string{"claude:b"}, map[string]string{"claude:b": "current"},
	) {
		t.Fatal("cache without provenance should not cover a revisioned source")
	}
	if entry.CoversCurrent([]string{"claude:a"}, map[string]string{}) {
		t.Fatal("revisioned cache must not cover a source that is now unrevisioned")
	}
	if !(&Entry{ProviderData: map[string]*provider.UsageData{"claude": {}}}).CoversCurrent(
		[]string{"claude"}, map[string]string{},
	) {
		t.Fatal("legacy unrevisioned source should cover legacy unrevisioned cache")
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "" && contains(string(encoded), "/") {
		t.Fatal("cache revision must not expose a raw path")
	}
}

func TestCredentialRotationCannotReuseCachedAccountData(t *testing.T) {
	oldRevision := provider.CredentialSourceRevision("env-name\x00WORK_KEY", "account-a-secret")
	newRevision := provider.CredentialSourceRevision("env-name\x00WORK_KEY", "account-b-secret")
	if oldRevision == newRevision {
		t.Fatal("credential rotation did not change the source revision")
	}
	entry := Entry{
		ProviderData:    map[string]*provider.UsageData{"synthetic:work": {Provider: "synthetic", SourceID: "work"}},
		SourceRevisions: map[string]string{"synthetic:work": oldRevision},
	}
	if entry.CoversCurrent([]string{"synthetic:work"}, map[string]string{"synthetic:work": newRevision}) {
		t.Fatal("account B matched account A's cached usage")
	}
	for _, revision := range []string{oldRevision, newRevision} {
		if strings.Contains(revision, "account-") || strings.Contains(revision, "WORK_KEY") {
			t.Fatalf("source revision disclosed credential material: %q", revision)
		}
	}
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestHasStaleData(t *testing.T) {
	entry := Entry{
		ProviderData: map[string]*provider.UsageData{
			"openai": {Provider: "openai"},
			"claude": {Provider: "claude", Stale: true},
		},
	}

	if !entry.HasStaleData([]string{"claude"}) {
		t.Fatal("HasStaleData(claude) = false, want true")
	}
	if entry.HasStaleData([]string{"openai"}) {
		t.Fatal("HasStaleData(openai) = true, want false")
	}
	if entry.HasStaleData([]string{"gemini"}) {
		t.Fatal("HasStaleData(missing) = true, want false")
	}
}
