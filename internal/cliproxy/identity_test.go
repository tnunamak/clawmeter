package cliproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	// stall makes the read wait until its context ends, like a provider
	// that never answers.
	stall bool
}

func (f *identifiedProvider) AccountIdentity(ctx context.Context) (provider.AccountIdentity, error) {
	f.calls++
	if f.onIdentity != nil {
		f.onIdentity()
	}
	if f.stall {
		<-ctx.Done()
		return provider.AccountIdentity{}, ctx.Err()
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

// T3 lists accounts, then reads each one's usage in a second request. A new
// login between the two must not put the new login's numbers under the
// email listed for the old one.
func TestAReadForAnAccountListedBeforeANewLoginIsRefused(t *testing.T) {
	isolatedCacheHome(t)
	source := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "old@example.com"}}
	h := newHandler(t, []provider.Provider{source}, testMaxAge)
	listed := fileByID(t, authFiles(t, h), "claude:work")
	oldIndex, _ := listed["auth_index"].(string)
	alsoListed := indexFor(t, h, "claude:work")

	source.revision = "rev-claude-work-new-login"
	source.identity = provider.AccountIdentity{Email: "new@example.com"}
	writeCache(t, "claude:work", freshClaudeReading("work", time.Now()), source.revision)

	for _, index := range []string{oldIndex, alsoListed} {
		if env := callAPI(t, h, apiCallBody{AuthIndex: index, Method: http.MethodGet, URL: claudeUsageURL}); env.StatusCode == http.StatusOK {
			t.Fatalf("read for the old login's index status_code = 200, want refused; body = %s", env.Body)
		}
	}

	relisted := fileByID(t, authFiles(t, h), "claude:work")
	newIndex, _ := relisted["auth_index"].(string)
	if newIndex == oldIndex || relisted["email"] != "new@example.com" {
		t.Fatalf("after a new login the list is %v, want a new index and the new email", relisted)
	}
	if env := callAPI(t, h, apiCallBody{AuthIndex: newIndex, Method: http.MethodGet, URL: claudeUsageURL}); env.StatusCode != http.StatusOK {
		t.Fatalf("read for the new index status_code = %d, want 200", env.StatusCode)
	}
}

func TestStalledIdentityLookupsDoNotHoldUpTheList(t *testing.T) {
	isolatedCacheHome(t)
	oldBudget := identityBudget
	identityBudget = 100 * time.Millisecond
	t.Cleanup(func() { identityBudget = oldBudget })

	stalled := []*identifiedProvider{
		{fakeProvider: claudeSource("one"), stall: true},
		{fakeProvider: claudeSource("two"), stall: true},
		{fakeProvider: claudeSource("three"), stall: true},
	}
	codex := &identifiedProvider{fakeProvider: codexSource("plus"), identity: provider.AccountIdentity{Email: "plus@example.com"}}
	sources := []provider.Provider{stalled[0], stalled[1], stalled[2], codex}
	server := httptest.NewUnstartedServer(newHandler(t, sources, testMaxAge))
	// The hub's own server deadline is 10 seconds; this one is shorter than
	// three stalled lookups in a row.
	server.Config.WriteTimeout = time.Second
	server.Start()
	defer server.Close()

	request, _ := http.NewRequest(http.MethodGet, server.URL+authFilesPath, nil)
	request.Header.Set("Authorization", "Bearer "+testManagementKey)
	started := time.Now()
	client := server.Client()
	client.Timeout = 2 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("auth-files: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if elapsed := time.Since(started); response.StatusCode != http.StatusOK || elapsed > 500*time.Millisecond {
		t.Fatalf("auth-files = %d after %s, want 200 within the identity budget; body = %s", response.StatusCode, elapsed, body)
	}
	for _, id := range []string{"claude:one", "claude:two", "claude:three", "codex:plus"} {
		if !strings.Contains(string(body), `"id":"`+id+`"`) {
			t.Fatalf("auth-files lacks %s: %s", id, body)
		}
	}
	if strings.Count(string(body), `"email"`) != 1 {
		t.Fatalf("auth-files = %s, want an email only for the account that answered", body)
	}
}

func TestAFailedIdentityLookupIsRetriedLater(t *testing.T) {
	source := &identifiedProvider{fakeProvider: claudeSource("work"), err: errors.New("profile: http 429")}
	h := newHandler(t, []provider.Provider{source}, testMaxAge)
	authFiles(t, h)
	authFiles(t, h)
	if source.calls != 1 {
		t.Fatalf("lookups = %d right after a failure, want 1", source.calls)
	}

	oldRetry := identityRetry
	identityRetry = 0
	t.Cleanup(func() { identityRetry = oldRetry })
	source.err = nil
	source.identity = provider.AccountIdentity{Email: "work@example.com"}
	if file := fileByID(t, authFiles(t, h), "claude:work"); file["email"] != "work@example.com" || source.calls != 2 {
		t.Fatalf("after the retry wait: file = %v, lookups = %d; want the email from a second lookup", file, source.calls)
	}
}

func TestAccountIdentityLeaksNoCredential(t *testing.T) {
	claude := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "work@example.com"}}
	codex := &identifiedProvider{fakeProvider: codexSource("plus"), identity: provider.AccountIdentity{
		Email: "plus@example.com", WorkspaceID: "acct-1", PlanType: "pro",
	}}
	body := authFilesRaw(t, newHandler(t, []provider.Provider{claude, codex}, testMaxAge))
	if !strings.Contains(body, "work@example.com") || !strings.Contains(body, "acct-1") {
		t.Fatalf("auth-files = %s, want the identities", body)
	}
	for _, secret := range []string{leakToken, claude.revision, codex.revision} {
		if strings.Contains(body, secret) {
			t.Fatalf("auth-files leaks %q: %s", secret, body)
		}
	}
}

// Two lists can look up one login at once. A lookup that fails after the
// other succeeded must not hide the known email.
func TestALateFailedLookupKeepsTheKnownIdentity(t *testing.T) {
	source := &identifiedProvider{fakeProvider: claudeSource("work"), identity: provider.AccountIdentity{Email: "work@example.com"}}
	h := newHandler(t, []provider.Provider{source}, testMaxAge)
	var concurrent http.Handler
	source.onIdentity = func() {
		if concurrent == nil {
			return
		}
		// While this lookup is in flight, another list succeeds.
		inner := concurrent
		concurrent = nil
		source.err = nil
		authFiles(t, inner)
		source.err = errors.New("timed out")
	}
	concurrent = h
	source.err = errors.New("timed out")
	if file := fileByID(t, authFiles(t, h), "claude:work"); file["email"] != nil {
		t.Fatalf("failed lookup gave an email: %v", file)
	}
	source.onIdentity = nil
	source.err = errors.New("must not be asked again")
	if file := fileByID(t, authFiles(t, h), "claude:work"); file["email"] != "work@example.com" {
		t.Fatalf("after a late failure the list is %v, want the email the other lookup found", file)
	}
}
