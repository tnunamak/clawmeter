package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func identitySource(t *testing.T, access string) *Provider {
	t.Helper()
	dir := t.TempDir()
	writeTestCredentials(t, dir, access, "refresh-identity")
	return NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
}

func profileServer(t *testing.T, status int, body string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer access-identity" {
			t.Errorf("Authorization = %q, want the source's token", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != betaHeader {
			t.Errorf("anthropic-beta = %q, want %q", got, betaHeader)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	old := profileEndpoint
	profileEndpoint = server.URL
	t.Cleanup(func() { profileEndpoint = old; server.Close() })
	return &calls
}

func TestAccountIdentityReadsTheTokensProfileEmail(t *testing.T) {
	profileServer(t, http.StatusOK, `{"account":{"uuid":"u","email":"  work@example.com "},"organization":{"uuid":"o"}}`)
	identity, err := identitySource(t, "access-identity").AccountIdentity(context.Background())
	if err != nil {
		t.Fatalf("AccountIdentity: %v", err)
	}
	if identity.Email != "work@example.com" || identity.WorkspaceID != "" || identity.PlanType != "" {
		t.Fatalf("identity = %+v, want only the trimmed email", identity)
	}
}

func TestAccountIdentityFailsWithoutAProfileEmail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":"invalid"}`},
		{"rate limited", http.StatusTooManyRequests, `{}`},
		{"no email", http.StatusOK, `{"account":{"uuid":"u"}}`},
		{"not json", http.StatusOK, `<html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profileServer(t, tc.status, tc.body)
			if identity, err := identitySource(t, "access-identity").AccountIdentity(context.Background()); err == nil {
				t.Fatalf("identity = %+v, want an error", identity)
			}
		})
	}
}

func TestAccountIdentityDoesNotRenewAnExpiredLogin(t *testing.T) {
	calls := profileServer(t, http.StatusOK, `{"account":{"email":"work@example.com"}}`)
	dir := t.TempDir()
	expired, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "access-identity", "refreshToken": "refresh-identity", "expiresAt": 1}})
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), expired, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
	if _, err := p.AccountIdentity(context.Background()); err == nil {
		t.Fatal("expired login gave an identity")
	}
	if calls.Load() != 0 {
		t.Fatalf("profile requests = %d, want 0 with an expired token", calls.Load())
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(before) != string(after) {
		t.Fatal("AccountIdentity changed the credentials file")
	}
}
