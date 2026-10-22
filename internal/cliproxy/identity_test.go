package cliproxy

import (
	"context"
	"errors"
	"testing"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// identifiedProvider is a fake source that also reports its login's identity.
type identifiedProvider struct {
	*fakeProvider
	identity provider.AccountIdentity
	err      error
	calls    int
	// onIdentity runs at each identity read, to change the source while it
	// is being read.
	onIdentity func()
}

func (f *identifiedProvider) AccountIdentity(context.Context) (provider.AccountIdentity, error) {
	f.calls++
	if f.onIdentity != nil {
		f.onIdentity()
	}
	return f.identity, f.err
}

func fileByID(t *testing.T, body authFilesBody, id string) map[string]any {
	t.Helper()
	for _, file := range body.Files {
		if file["id"] == id {
			return file
		}
	}
	t.Fatalf("auth-files has no %q: %+v", id, body.Files)
	return nil
}

func TestAuthFilesNameEachAccountsLogin(t *testing.T) {
	claude := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "work@example.com"}}
	codex := &identifiedProvider{fakeProvider: codexSource("plus"), identity: provider.AccountIdentity{
		Email: "plus@example.com", WorkspaceID: "acct-1", PlanType: "pro",
	}}
	body := authFiles(t, newHandler(t, []provider.Provider{claude, codex}, testMaxAge))

	claudeFile := fileByID(t, body, "claude:work")
	if claudeFile["email"] != "work@example.com" {
		t.Fatalf("claude email = %v", claudeFile["email"])
	}
	if _, ok := claudeFile["id_token"]; ok {
		t.Fatalf("claude account has an id_token: %v", claudeFile)
	}
	codexFile := fileByID(t, body, "codex:plus")
	tokens, _ := codexFile["id_token"].(map[string]any)
	if codexFile["email"] != "plus@example.com" || tokens["chatgpt_account_id"] != "acct-1" || tokens["plan_type"] != "pro" {
		t.Fatalf("codex file = %v", codexFile)
	}
}

func TestAccountIdentityIsWithheldWhenItCannotBeBound(t *testing.T) {
	t.Run("read fails", func(t *testing.T) {
		source := &identifiedProvider{fakeProvider: claudeSource("work"), err: errors.New("profile: http 429")}
		file := fileByID(t, authFiles(t, newHandler(t, []provider.Provider{source}, testMaxAge)), "claude:work")
		if len(file) != 3 {
			t.Fatalf("file = %v, want only id, auth_index and provider", file)
		}
	})
	t.Run("login changes during the read", func(t *testing.T) {
		source := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "old@example.com"}}
		source.onIdentity = func() { source.revision = "rev-claude-work-another-login" }
		file := fileByID(t, authFiles(t, newHandler(t, []provider.Provider{source}, testMaxAge)), "claude:work")
		if _, ok := file["email"]; ok {
			t.Fatalf("file = %v, want no email from a login that changed mid-read", file)
		}
	})
	t.Run("source without a revision", func(t *testing.T) {
		source := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "work@example.com"}}
		source.revision = ""
		file := fileByID(t, authFiles(t, newHandler(t, []provider.Provider{source}, testMaxAge)), "claude:work")
		if _, ok := file["email"]; ok || source.calls != 0 {
			t.Fatalf("file = %v after %d reads, want no email and no read", file, source.calls)
		}
	})
	t.Run("binding lost", func(t *testing.T) {
		source := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "work@example.com"}}
		h := newHandler(t, []provider.Provider{source}, testMaxAge)
		source.unbound = true
		file := fileByID(t, authFiles(t, h), "claude:work")
		if _, ok := file["email"]; ok {
			t.Fatalf("file = %v, want no email once the source is unbound", file)
		}
	})
}

func TestAccountIdentityIsReadOncePerLogin(t *testing.T) {
	source := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "work@example.com"}}
	h := newHandler(t, []provider.Provider{source}, testMaxAge)
	authFiles(t, h)
	authFiles(t, h)
	if source.calls != 1 {
		t.Fatalf("identity reads = %d for one login, want 1", source.calls)
	}

	source.revision = "rev-claude-work-new-login"
	source.identity = provider.AccountIdentity{Email: "new@example.com"}
	if file := fileByID(t, authFiles(t, h), "claude:work"); file["email"] != "new@example.com" {
		t.Fatalf("email after a new login = %v, want the new login's", file["email"])
	}
	if source.calls != 2 {
		t.Fatalf("identity reads = %d after a new login, want 2", source.calls)
	}

	// A failed read after a new login never falls back to the old identity.
	source.revision = "rev-claude-work-third-login"
	source.err = errors.New("offline")
	if file := fileByID(t, authFiles(t, h), "claude:work"); file["email"] != nil {
		t.Fatalf("email = %v when the new login's identity is unknown, want none", file["email"])
	}
}
