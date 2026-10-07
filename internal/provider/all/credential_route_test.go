package all

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/antigravity"
	"github.com/tnunamak/clawmeter/internal/provider/gemini"
	"github.com/tnunamak/clawmeter/internal/provider/openai"
	"github.com/tnunamak/clawmeter/internal/provider/xai"
)

// limitedRoute wraps a real source, keeps its real CredentialRoute, and answers
// every fetch with a 429 while counting the requests that reach the provider.
type limitedRoute struct {
	provider.Provider
	calls *atomic.Int32
}

func (p limitedRoute) CredentialRoute() string {
	return p.Provider.(provider.CredentialRouteCapability).CredentialRoute()
}

func (p limitedRoute) FetchUsage(context.Context) (*provider.UsageData, error) {
	p.calls.Add(1)
	return nil, &provider.RateLimitError{}
}

// A source that keeps its ID but is repointed to another credential location is
// another account: it must not inherit the old account's 429 backoff. A token
// refresh on the same location must not end the backoff.
func TestRepointedCredentialRouteDoesNotInheritBackoff(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg := config.ProviderConfig{}
	cases := []struct {
		name   string
		kind   string
		file   string // credential file inside a directory ref; "" when ref is the file
		newSrc func(config.SourceConfig) provider.Provider
	}{
		{"gemini", "config-dir", "oauth_creds.json", func(s config.SourceConfig) provider.Provider { return gemini.NewSource(cfg, s) }},
		{"antigravity", "token-file", "", func(s config.SourceConfig) provider.Provider { return antigravity.NewSource(s) }},
		{"xai", "grok-home", "auth.json", func(s config.SourceConfig) provider.Provider { return xai.NewSource(cfg, s) }},
		{"openai", "codex-home", "auth.json", func(s config.SourceConfig) provider.Provider {
			p, err := openai.NewSource(cfg, s)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, second := t.TempDir(), t.TempDir()
			ref := func(root string) string {
				if tc.file == "" {
					return filepath.Join(root, "token.json")
				}
				return root
			}
			credFile := func(root string) string {
				if tc.file == "" {
					return ref(root)
				}
				return filepath.Join(root, tc.file)
			}
			var calls atomic.Int32
			fetch := func(root string) {
				t.Helper()
				src := tc.newSrc(config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: tc.kind, Ref: ref(root)}})
				if _, err := provider.FetchSource(context.Background(), limitedRoute{Provider: src, calls: &calls}); err != nil {
					t.Fatal(err)
				}
			}
			fetch(first)
			if got := calls.Load(); got != 1 {
				t.Fatalf("first route requests = %d, want 1", got)
			}
			if err := os.WriteFile(credFile(first), []byte("refreshed"), 0o600); err != nil {
				t.Fatal(err)
			}
			fetch(first)
			if got := calls.Load(); got != 1 {
				t.Fatalf("requests after token refresh = %d, want backoff to hold at 1", got)
			}
			fetch(second)
			if got := calls.Load(); got != 2 {
				t.Fatalf("repointed route requests = %d, want 2; it inherited the old account's backoff", got)
			}
		})
	}
}
