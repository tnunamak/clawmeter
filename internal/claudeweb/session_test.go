package claudeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResultRequiresClaudeOriginAndMatchingNonce(t *testing.T) {
	isolateUserCache(t)
	s := &Session{nonce: "test-nonce", result: make(chan GrantSummary, 1), resetCreditsTarget: "claude:odl"}
	handler := s.handler()

	request := func(origin, nonce string) *httptest.ResponseRecorder {
		expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
		payload, _ := json.Marshal(resultPayload{
			Nonce: nonce, Count: 1, Earliest: expires, Credits: []string{expires},
		})
		req := httptest.NewRequest(http.MethodPost, "/result", strings.NewReader(string(payload)))
		req.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	if got := request("https://evil.example", "test-nonce").Code; got != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", got)
	}
	if got := request(claudeOrigin, "wrong").Code; got != http.StatusBadRequest {
		t.Fatalf("bad nonce status = %d, want 400", got)
	}
	if got := request(claudeOrigin, "test-nonce").Code; got != http.StatusOK {
		t.Fatalf("valid result status = %d, want 200", got)
	}
	got, err := s.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Credits) != 1 || got.Credits[0].IsZero() {
		t.Fatalf("result = %+v, want one grant with expiry", got)
	}
	if got.ResetCreditsTarget != "claude:odl" {
		t.Fatalf("result target = %q, want selected Claude source", got.ResetCreditsTarget)
	}
	saved, err := ReadSummaryFor("claude:odl")
	if err != nil || len(saved.Credits) != 1 {
		t.Fatalf("ReadSummary() = %+v, %v; want persisted grant", saved, err)
	}
	if saved.ResetCreditsTarget != "claude:odl" {
		t.Fatalf("saved target = %q, want selected Claude source", saved.ResetCreditsTarget)
	}
}

func TestStartRequiresAnExplicitClaudeSource(t *testing.T) {
	for _, target := range []string{"", "codex", "claude:"} {
		if _, err := StartForSource(target, ""); err == nil {
			t.Fatalf("StartForSource(%q) accepted a missing or invalid source; observations must be account-scoped", target)
		}
	}
}

func TestResultAcceptsEmptyInventoryAndRejectsExtraFields(t *testing.T) {
	isolateUserCache(t)
	s := &Session{nonce: "n", result: make(chan GrantSummary, 1), resetCreditsTarget: "claude"}
	handler := s.handler()
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/result", strings.NewReader(body))
		req.Header.Set("Origin", claudeOrigin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response.Code
	}

	if got := post(`{"nonce":"n","count":0,"earliest":""}`); got != http.StatusOK {
		t.Fatalf("empty inventory status = %d, want 200", got)
	}
	if got := post(`{"nonce":"n","count":0,"earliest":"","account_id":"private"}`); got != http.StatusBadRequest {
		t.Fatalf("extra field status = %d, want 400", got)
	}
	if got := post(`{"nonce":"n","count":1,"earliest":"2000-01-01T00:00:00Z"}`); got != http.StatusBadRequest {
		t.Fatalf("expired credit status = %d, want 400", got)
	}
}

func TestResultAcceptsExistingBookmarkSummaryPayload(t *testing.T) {
	isolateUserCache(t)
	s := &Session{nonce: "n", result: make(chan GrantSummary, 1), resetCreditsTarget: "claude"}
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest(http.MethodPost, "/result", strings.NewReader(`{"nonce":"n","count":1,"earliest":"`+expires+`"}`))
	req.Header.Set("Origin", claudeOrigin)
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy bookmark result status = %d, want 200: %s", response.Code, response.Body.String())
	}
	summary, err := ReadSummaryFor("claude")
	if err != nil {
		t.Fatal(err)
	}
	if summary.AvailableCount != 1 || !summary.EarliestExpiresAt.After(time.Now()) || len(summary.Credits) != 0 {
		t.Fatalf("legacy summary = %+v, want count and earliest expiry without invented per-credit detail", summary)
	}
}

func TestGrantInventoryCountsOnlyStartedUnpausedUnexpiredCredits(t *testing.T) {
	isolateUserCache(t)
	now := time.Now().UTC()
	paused := true
	active := false
	s := &Session{nonce: "n", result: make(chan GrantSummary, 1), resetCreditsTarget: "claude:odl", sourceRevision: "source-revision"}
	grants := []grantPayload{
		{ResetsLeft: 2, StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano), Paused: &active},
		{ResetsLeft: 1, StartsAt: now.Add(time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(48 * time.Hour).Format(time.RFC3339Nano), Paused: &active},
		{ResetsLeft: 3, StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(48 * time.Hour).Format(time.RFC3339Nano), Paused: &paused},
		{ResetsLeft: 1, StartsAt: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), EndsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Paused: &active},
	}
	body, err := json.Marshal(resultPayload{Nonce: "n", Grants: grants})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/result", bytes.NewReader(body))
	req.Header.Set("Origin", claudeOrigin)
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("grant result status = %d, want 200: %s", response.Code, response.Body.String())
	}
	summary, err := ReadSummaryFor("claude:odl")
	if err != nil {
		t.Fatal(err)
	}
	if summary.AvailableCount != 2 || len(summary.Credits) != 2 || !summary.EarliestExpiresAt.Equal(now.Add(24*time.Hour).Truncate(time.Nanosecond)) {
		t.Fatalf("saved credits = %+v, want only 2 active resets", summary)
	}
	if summary.SourceRevision != "source-revision" {
		t.Fatalf("saved source revision = %q, want source binding", summary.SourceRevision)
	}
	if strings.Contains(response.Body.String(), "ODL") || strings.Contains(response.Body.String(), "source_label") {
		t.Fatalf("handoff response exposed local source metadata: %s", response.Body.String())
	}
}

func TestChallengeExposesOnlyOneTimeNonce(t *testing.T) {
	s := &Session{nonce: "one-time", sourceRevision: "private-revision"}
	req := httptest.NewRequest(http.MethodGet, "/challenge", nil)
	req.Header.Set("Origin", claudeOrigin)
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("challenge status = %d", response.Code)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload["nonce"] != "one-time" {
		t.Fatalf("challenge exposed source metadata: %#v", payload)
	}
}

func TestGrantInventoryRejectsUnknownPauseOrSchedule(t *testing.T) {
	now := time.Now().UTC()
	_, err := activeGrantExpiries([]grantPayload{{ResetsLeft: 1, StartsAt: now.Format(time.RFC3339Nano), EndsAt: now.Add(time.Hour).Format(time.RFC3339Nano)}}, now)
	if err == nil {
		t.Fatal("missing paused state accepted")
	}
	paused := false
	_, err = activeGrantExpiries([]grantPayload{{ResetsLeft: 1, EndsAt: now.Add(time.Hour).Format(time.RFC3339Nano), Paused: &paused}}, now)
	if err == nil {
		t.Fatal("missing start time accepted")
	}
}

func TestSnapshotsRemainSeparatePerSource(t *testing.T) {
	isolateUserCache(t)
	defaultExpiry := time.Now().Add(24 * time.Hour).UTC()
	odlExpiry := time.Now().Add(48 * time.Hour).UTC()
	if err := WriteSummaryFor("claude", GrantSummary{Credits: []time.Time{defaultExpiry}, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := WriteSummaryFor("claude:odl", GrantSummary{Credits: []time.Time{odlExpiry}, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	gotDefault, err := ReadSummaryFor("claude")
	if err != nil || !gotDefault.Credits[0].Equal(defaultExpiry) {
		t.Fatalf("Default snapshot changed: %+v, %v", gotDefault, err)
	}
	gotODL, err := ReadSummaryFor("claude:odl")
	if err != nil || !gotODL.Credits[0].Equal(odlExpiry) {
		t.Fatalf("ODL snapshot changed: %+v, %v", gotODL, err)
	}
	if _, err := ReadSummaryFor("claude:work"); err == nil {
		t.Fatal("a source without its own observation read another source's snapshot")
	}
}

func TestBookmarkletUsesOnlyClaudeUsageReadAndAllowlistedResult(t *testing.T) {
	got := Bookmarklet()
	if strings.ContainsAny(got, "\r\n") {
		t.Fatal("bookmarklet URL contains a line break")
	}
	for _, want := range []string{
		"/api/organizations",
		"lastActiveOrg",
		"usage",
		"cedar_ember",
		"resets_left",
		"ends_at",
		"grants",
		"starts_at",
		"paused",
		"window.confirm",
		"/challenge",
		"/result",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bookmarklet missing %q", want)
		}
	}
	if strings.Contains(got, "source_label") {
		t.Fatal("bookmarklet must not receive local source labels")
	}
	for _, forbidden := range []string{"/limit-reset", "/consume", "sessionKey", "cf_clearance", "localStorage", "body: JSON.stringify({ nonce, grants, organizationID", "body: JSON.stringify({ nonce, grants, account"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("bookmarklet includes forbidden access %q", forbidden)
		}
	}
}

func TestSummaryStorageContainsOnlyResetMetadata(t *testing.T) {
	isolateUserCache(t)
	summary := GrantSummary{
		Credits:            []time.Time{time.Now().Add(24 * time.Hour).UTC()},
		ObservedAt:         time.Now().UTC(),
		ResetCreditsTarget: "claude:odl",
	}
	if err := WriteSummary(summary); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSummaryFor("claude:odl")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Credits) != 1 || !got.Credits[0].Equal(summary.Credits[0]) || !got.ObservedAt.Equal(summary.ObservedAt) || got.ResetCreditsTarget != summary.ResetCreditsTarget {
		t.Fatalf("ReadSummary() = %+v, want %+v", got, summary)
	}
	path, err := summaryPathFor("claude:odl")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sessionKey", "account", "organization", "nonce"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("saved reset data contains %q", forbidden)
		}
	}
}

func TestWriteSummaryRejectsNonClaudeResetTarget(t *testing.T) {
	isolateUserCache(t)
	if err := WriteSummary(GrantSummary{ObservedAt: time.Now(), ResetCreditsTarget: "codex:default"}); err == nil {
		t.Fatal("WriteSummary accepted a non-Claude reset target")
	}
}

func TestBookmarkSetupStateIsSharedAcrossClaudeSources(t *testing.T) {
	isolateUserCache(t)
	if SetupStarted() {
		t.Fatal("setup marker exists before setup is opened")
	}
	if err := MarkSetupStarted(); err != nil {
		t.Fatal(err)
	}
	if !SetupStarted() {
		t.Fatal("saved bookmark setup should be reusable across Claude sources")
	}
}

func TestBrowserSnapshotDoesNotProveBookmarkStillExists(t *testing.T) {
	isolateUserCache(t)
	if err := WriteSummaryFor("claude", GrantSummary{AvailableCount: 1, EarliestExpiresAt: time.Now().Add(time.Hour), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if SetupStarted() {
		t.Fatal("an old browser snapshot must not imply the bookmark is still available")
	}
}

func isolateUserCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("HOME", dir)
}

func TestSetupPageEmbedsBookmarkAndCopyFallback(t *testing.T) {
	var page bytes.Buffer
	if err := setupPage.Execute(&page, setupPageData{Bookmark: template.URL(Bookmarklet())}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`href="javascript:`,
		"Clawmeter reset check",
		"Copy bookmark URL",
		"bookmarks bar can stay hidden",
		"Do not click the link on this setup page",
		"bookmark manager, add a bookmark and paste this into its URL field",
		"Check Claude reset",
		"document.execCommand(\"copy\")",
		"make sure the browser is signed into the same Claude account",
		"reused for any Claude source",
	} {
		if !strings.Contains(page.String(), want) {
			t.Errorf("setup page missing %q", want)
		}
	}
}

func TestCloseAfterWaitStillDeliversBrowserResponse(t *testing.T) {
	isolateUserCache(t)
	for i := 0; i < 100; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		s := &Session{nonce: "n", result: make(chan GrantSummary, 1), resetCreditsTarget: "claude"}
		s.server = &http.Server{Handler: s.handler()}
		go func() { _ = s.server.Serve(listener) }()
		go func() {
			_, _ = s.Wait(context.Background())
			s.Close()
		}()
		expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
		req, _ := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/result", strings.NewReader(`{"nonce":"n","count":1,"earliest":"`+expires+`"}`))
		req.Header.Set("Origin", claudeOrigin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("check %d: browser saw failed handoff: %v", i, err)
		}
		_, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("check %d: status %d, body err %v", i, resp.StatusCode, err)
		}
	}
}
