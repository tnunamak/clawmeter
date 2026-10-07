package claudeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

const testNonce = "test-nonce"

var testAccounts = []Account{
	{Key: "claude", Label: "Default", OrgUUID: "org-default"},
	{Key: "claude:odl", Label: "ODL", OrgUUID: "org-odl"},
	{Key: "claude:spare", Label: "Spare"}, // no org recorded yet
}

func serve(t *testing.T, s *Session, method, path, origin, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = listenAddress
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, req)
	return response
}

func activeGrant(expires time.Time, resets int) grantPayload {
	paused := false
	return grantPayload{ResetsLeft: resets, StartsAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), EndsAt: expires.UTC().Format(time.RFC3339Nano), Paused: &paused}
}

func resultBody(t *testing.T, nonce, org string, grants ...grantPayload) string {
	t.Helper()
	if grants == nil {
		grants = []grantPayload{}
	}
	body, err := json.Marshal(resultPayload{Nonce: nonce, Account: AccountHash(nonce, org), Grants: grants})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func message(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response %d is not a JSON message: %q", response.Code, response.Body.String())
	}
	return body["message"]
}

func TestOrgHashRoutesResultToTheMatchingSource(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	expires := time.Now().Add(24 * time.Hour)
	response := serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-odl", activeGrant(expires, 1)))
	if response.Code != http.StatusOK || message(t, response) != "Found 1 reset for ODL." {
		t.Fatalf("result = %d %q, want 200 \"Found 1 reset for ODL.\"", response.Code, response.Body.String())
	}
	got, err := s.Wait(context.Background())
	if err != nil || got.ResetCreditsTarget != "claude:odl" {
		t.Fatalf("Wait() = %+v, %v; want the ODL source", got, err)
	}
	saved, err := ReadSummaryFor("claude:odl")
	if err != nil || len(saved.Credits) != 1 || !saved.MatchesOrg("org-odl") || saved.MatchesOrg("org-default") {
		t.Fatalf("saved = %+v, %v; want one credit bound to org-odl", saved, err)
	}
	if _, err := ReadSummaryFor("claude"); err == nil {
		t.Fatal("result also saved under the Default source")
	}
	if got := s.currentStatus(); got != (Status{State: stateDone, Message: "Found 1 reset for ODL. You can close this tab."}) {
		t.Fatalf("page status = %+v", got)
	}
}

func TestOrgHashWithNoMatchingSourceIsRejectedAndCheckKeepsWaiting(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	response := serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-unknown"))
	if response.Code != http.StatusNotFound || message(t, response) != msgNotSetUp {
		t.Fatalf("result = %d %q, want 404 not-set-up", response.Code, response.Body.String())
	}
	if got := s.currentStatus(); got.State != stateRetry || got.Message != msgNotSetUp {
		t.Fatalf("page status = %+v, want retry with the same message", got)
	}
	// The user switches accounts and clicks again: the same session accepts it.
	if response := serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-default")); response.Code != http.StatusOK {
		t.Fatalf("retry after switching accounts = %d", response.Code)
	}
}

func TestOrgHashMatchingTwoSourcesIsRejectedAsAmbiguous(t *testing.T) {
	isolateUserCache(t)
	accounts := []Account{{Key: "claude", Label: "Default", OrgUUID: "org-same"}, {Key: "claude:odl", Label: "ODL", OrgUUID: "org-same"}}
	s := newSession(testNonce, accounts)
	response := serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-same"))
	if response.Code != http.StatusConflict || message(t, response) != msgAmbiguous {
		t.Fatalf("result = %d %q, want 409 ambiguous", response.Code, response.Body.String())
	}
	for _, key := range []string{"claude", "claude:odl"} {
		if _, err := ReadSummaryFor(key); err == nil {
			t.Fatalf("ambiguous result saved under %s", key)
		}
	}
}

func TestResultRequiresClaudeOriginLoopbackHostAndNonce(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	body := resultBody(t, testNonce, "org-odl")
	if got := serve(t, s, http.MethodPost, "/result", "https://evil.example", body).Code; got != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, want 403", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/result", strings.NewReader(body))
	req.Host = "rebound.example:17343"
	req.Header.Set("Origin", claudeOrigin)
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-loopback Host = %d, want 403", response.Code)
	}
	if got := serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, "old-nonce", "org-odl")); got.Code != http.StatusBadRequest || message(t, got) != msgEnded {
		t.Fatalf("stale nonce = %d %q", got.Code, got.Body.String())
	}
}

func TestResultIsSingleUse(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	body := resultBody(t, testNonce, "org-odl")
	if got := serve(t, s, http.MethodPost, "/result", claudeOrigin, body).Code; got != http.StatusOK {
		t.Fatalf("first result = %d", got)
	}
	if got := serve(t, s, http.MethodPost, "/result", claudeOrigin, body); got.Code != http.StatusConflict || message(t, got) != msgDone {
		t.Fatalf("second result = %d %q, want 409 already saved", got.Code, got.Body.String())
	}
}

func TestOldBookmarkPayloadAndExtraFieldsAreRejected(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	for _, body := range []string{
		`{"nonce":"test-nonce","count":1,"earliest":"` + expires + `"}`,             // v0.35.0 bookmark
		`{"nonce":"test-nonce","grants":[]}`,                                        // no account proof
		`{"nonce":"test-nonce","account":"x","grants":[],"organization":"org-odl"}`, // raw org
	} {
		if got := serve(t, s, http.MethodPost, "/result", claudeOrigin, body); got.Code != http.StatusBadRequest || message(t, got) != msgOutdated {
			t.Errorf("body %s: %d %q, want 400 out-of-date bookmark", body, got.Code, got.Body.String())
		}
	}
}

func TestGrantInventoryCountsOnlyStartedUnpausedUnexpiredCredits(t *testing.T) {
	now := time.Now().UTC()
	paused, active := true, false
	credits, err := activeGrantExpiries([]grantPayload{
		{ResetsLeft: 2, StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano), Paused: &active},
		{ResetsLeft: 1, StartsAt: now.Add(time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(48 * time.Hour).Format(time.RFC3339Nano), Paused: &active},
		{ResetsLeft: 3, StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(48 * time.Hour).Format(time.RFC3339Nano), Paused: &paused},
		{ResetsLeft: 1, StartsAt: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Paused: &active},
	}, now)
	if err != nil || len(credits) != 2 {
		t.Fatalf("credits = %v, %v; want only the 2 active resets", credits, err)
	}
}

func TestGrantInventoryRejectsUnknownPauseOrSchedule(t *testing.T) {
	now := time.Now().UTC()
	if _, err := activeGrantExpiries([]grantPayload{{ResetsLeft: 1, StartsAt: now.Format(time.RFC3339Nano), EndsAt: now.Add(time.Hour).Format(time.RFC3339Nano)}}, now); err == nil {
		t.Fatal("missing paused state accepted")
	}
	paused := false
	if _, err := activeGrantExpiries([]grantPayload{{ResetsLeft: 1, EndsAt: now.Add(time.Hour).Format(time.RFC3339Nano), Paused: &paused}}, now); err == nil {
		t.Fatal("missing start time accepted")
	}
}

func TestChallengeExposesOnlyTheNonce(t *testing.T) {
	s := newSession(testNonce, testAccounts)
	response := serve(t, s, http.MethodGet, "/challenge", claudeOrigin, "")
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["nonce"] != testNonce {
		t.Fatalf("challenge = %d %q", response.Code, response.Body.String())
	}
	if serve(t, s, http.MethodGet, "/challenge", "https://evil.example", "").Code != http.StatusForbidden {
		t.Fatal("challenge answered a foreign origin")
	}
}

func TestStatusIsNotReadableByOtherSites(t *testing.T) {
	s := newSession(testNonce, testAccounts)
	response := serve(t, s, http.MethodGet, "/status", "https://evil.example", "")
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("status endpoint sent CORS headers")
	}
	var got Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got != (Status{State: stateWaiting, Message: "Waiting for the bookmark…"}) {
		t.Fatalf("status = %q", response.Body.String())
	}
}

func TestSavedSnapshotContainsNoRawOrganizationID(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-odl", activeGrant(time.Now().Add(time.Hour), 1)))
	path, err := summaryPathFor("claude:odl")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"org-odl", testNonce, "ODL", "sessionKey"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("saved snapshot contains %q: %s", forbidden, data)
		}
	}
}

func TestBookmarkIsProvenOnlyByAResult(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	serve(t, s, http.MethodGet, "/check", "", "")
	if BookmarkProven() {
		t.Fatal("opening the setup page proved the bookmark")
	}
	// A rejected result still proves the bookmark runs.
	serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-unknown"))
	if !BookmarkProven() {
		t.Fatal("a result from the bookmark did not prove it")
	}
	// A later check the bookmark never answered: show setup again.
	silent := newSession("other", testAccounts)
	silent.timeOut()
	if BookmarkProven() {
		t.Fatal("bookmark still proven after a check it never answered")
	}
	if _, err := silent.Wait(context.Background()); err != ErrTimedOut {
		t.Fatalf("Wait() after timeout = %v, want ErrTimedOut", err)
	}
	if got := silent.currentStatus(); got != (Status{State: stateEnded, Message: msgTimedOut}) {
		t.Fatalf("page status after timeout = %+v", got)
	}
	// A check the bookmark answered without a match does not unprove it.
	if err := recordBookmarkWorked(time.Now()); err != nil {
		t.Fatal(err)
	}
	answered := newSession("third", testAccounts)
	serve(t, answered, http.MethodGet, "/challenge", claudeOrigin, "")
	answered.timeOut()
	if !BookmarkProven() {
		t.Fatal("a check the bookmark answered unproved it")
	}
}

func renderCheckPage(t *testing.T, proven bool) string {
	t.Helper()
	var page bytes.Buffer
	if err := checkPage.Execute(&page, checkPageData{Bookmark: template.URL(Bookmarklet()), UsageURL: UsageURL, Proven: proven, Ended: msgEnded}); err != nil {
		t.Fatal(err)
	}
	return page.String()
}

func TestCheckPageShowsAllStepsUntilTheBookmarkIsProven(t *testing.T) {
	first := renderCheckPage(t, false)
	for _, want := range []string{
		"<h1>Check Claude resets</h1>",
		`href="javascript:`,
		">Clawmeter resets</a> to your bookmarks bar.",
		"Copy bookmark URL</button> and paste it as a new bookmark's URL.",
		`href="https://claude.ai/settings/usage" target="_blank"`,
		">Open Claude Usage</a>",
		"<li>Click the bookmark there.</li>",
		"Waiting for the bookmark…",
		"If your browser asks to let claude.ai access your local network, allow it.",
		`fetch("/status"`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first-run page missing %q", want)
		}
	}
	proven := renderCheckPage(t, true)
	for _, want := range []string{">Open Claude Usage</a>", "Then click the Clawmeter resets bookmark there.", "<summary>Bookmark missing? Save it again</summary>", "Copy bookmark URL"} {
		if !strings.Contains(proven, want) {
			t.Errorf("proven page missing %q", want)
		}
	}
	if strings.Contains(proven, "<ol>") || strings.Contains(proven, "local network") {
		t.Error("proven page still shows first-run steps")
	}
}

func TestBookmarkletSendsOnlyAHashedAccount(t *testing.T) {
	got := Bookmarklet()
	if strings.ContainsAny(got, "\r\n") {
		t.Fatal("bookmarklet URL contains a line break")
	}
	for _, want := range []string{"/api/organizations", "lastActiveOrg", "cedar_ember", "crypto.subtle.digest", `clawmeter-claude-reset\u0000${nonce}\u0000${organizationID}`, "body: JSON.stringify({ nonce, account, grants })", "/challenge", "/result"} {
		if !strings.Contains(got, want) {
			t.Errorf("bookmarklet missing %q", want)
		}
	}
	for _, forbidden := range []string{"window.confirm", "organizationID }", "organizationID, ", "/limit-reset", "/consume", "sessionKey", "cf_clearance", "localStorage"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("bookmarklet includes %q", forbidden)
		}
	}
}

// TestBookmarkletRunsAndItsHashMatchesGo executes the real bookmarklet in
// Node with a fake claude.ai and a fake Clawmeter, then checks that what it
// posts is exactly what the Go handler expects.
func TestBookmarkletRunsAndItsHashMatchesGo(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	harness := `
const posted = [], alerts = [];
globalThis.location = { origin: "https://claude.ai" };
globalThis.document = { cookie: "foo=1; lastActiveOrg=org-odl" };
globalThis.window = {};
globalThis.alert = message => alerts.push(message);
const json = (status, body) => ({ ok: status < 300, status, json: async () => body });
globalThis.fetch = async (url, options = {}) => {
  if (url === "/api/organizations") return json(200, [{ uuid: "org-default" }, { uuid: "org-odl" }]);
  if (url.startsWith("/api/organizations/org-odl/usage")) return json(200, { cedar_ember: { eligible: true, grants: [
    { resets_left: 1, starts_at: "2026-01-01T00:00:00Z", ends_at: "2099-01-01T00:00:00Z", paused: false, extra: "dropped" } ] } });
  if (url === "http://127.0.0.1:17343/challenge") return json(200, { nonce: "test-nonce" });
  if (url === "http://127.0.0.1:17343/result") { posted.push(options.body); return json(200, { message: "Found 1 reset for ODL." }); }
  throw new Error("unexpected fetch " + url);
};
process.on("beforeExit", () => { console.log(JSON.stringify({ posted, alerts })); process.exit(0); });
`
	script := filepath.Join(t.TempDir(), "run.js")
	if err := os.WriteFile(script, []byte(harness+strings.TrimPrefix(Bookmarklet(), "javascript:")), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var run struct{ Posted, Alerts []string }
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("harness output %q: %v", out, err)
	}
	if len(run.Alerts) != 1 || run.Alerts[0] != "Clawmeter: Found 1 reset for ODL." || len(run.Posted) != 1 {
		t.Fatalf("run = %+v", run)
	}
	if strings.Contains(run.Posted[0], "org-odl") {
		t.Fatalf("bookmarklet posted the raw organization ID: %s", run.Posted[0])
	}
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	response := serve(t, s, http.MethodPost, "/result", claudeOrigin, run.Posted[0])
	if response.Code != http.StatusOK || message(t, response) != "Found 1 reset for ODL." {
		t.Fatalf("Go handler rejected the real bookmarklet payload: %d %q (%s)", response.Code, response.Body.String(), run.Posted[0])
	}
}

func TestResultAfterTimeoutIsNotReportedAsSaved(t *testing.T) {
	isolateUserCache(t)
	s := newSession(testNonce, testAccounts)
	s.mu.Lock()
	s.status = Status{State: stateEnded, Message: msgTimedOut}
	s.mu.Unlock()
	got := serve(t, s, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-odl"))
	if got.Code == http.StatusOK || message(t, got) != msgTimedOut {
		t.Fatalf("late result = %d %q, want the timeout message", got.Code, got.Body.String())
	}
	if _, err := ReadSummaryFor("claude:odl"); err == nil {
		t.Fatal("late result was saved")
	}
}
