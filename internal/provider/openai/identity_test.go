package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func fakeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func identityHome(t *testing.T, idToken, savedAccount string) *Provider {
	t.Helper()
	dir := t.TempDir()
	auth, _ := json.Marshal(map[string]any{"tokens": map[string]any{
		"access_token": "access-identity", "id_token": idToken, "account_id": savedAccount,
	}})
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewSource(config.ProviderConfig{}, source("work", dir))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func chatGPTClaims(email, account, plan string) map[string]any {
	return map[string]any{
		"email":                       email,
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": account, "chatgpt_plan_type": plan},
	}
}

func TestAccountIdentityReadsTheSavedIDToken(t *testing.T) {
	p := identityHome(t, fakeIDToken(t, chatGPTClaims("work@example.com", "acct-1", "pro")), "acct-1")
	identity, err := p.AccountIdentity(context.Background())
	if err != nil {
		t.Fatalf("AccountIdentity: %v", err)
	}
	if identity.Email != "work@example.com" || identity.WorkspaceID != "acct-1" || identity.PlanType != "pro" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestAccountIdentityFallsBackToTheSavedAccount(t *testing.T) {
	p := identityHome(t, fakeIDToken(t, map[string]any{"email": "work@example.com"}), "acct-1")
	identity, err := p.AccountIdentity(context.Background())
	if err != nil {
		t.Fatalf("AccountIdentity: %v", err)
	}
	if identity.WorkspaceID != "acct-1" {
		t.Fatalf("workspace = %q, want the saved account", identity.WorkspaceID)
	}
}

func TestAccountIdentityRejectsUnusableLogins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		idToken string
		saved   string
	}{
		{"accounts disagree", fakeIDToken(t, chatGPTClaims("work@example.com", "acct-1", "pro")), "acct-2"},
		{"no email", fakeIDToken(t, chatGPTClaims("", "acct-1", "pro")), "acct-1"},
		{"no ID token", "", "acct-1"},
		{"not a JWT", "not-a-jwt", "acct-1"},
		{"bad payload", "a.!!!.c", "acct-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if identity, err := identityHome(t, tc.idToken, tc.saved).AccountIdentity(context.Background()); err == nil {
				t.Fatalf("identity = %+v, want an error", identity)
			}
		})
	}
}
