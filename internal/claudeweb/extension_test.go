package claudeweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func extensionBody(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(creditsBody(t, testNonce)), &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "nonce")
	body["observed_at"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	body["link"] = map[string]any{"organization": "org-odl", "plan": "max_20x", "monthly_credit": 20000}
	return body
}

func extensionRequest(handler http.Handler, method, path, host, origin, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+extensionAddress+path, strings.NewReader(body))
	request.Host = host
	request.Header.Set("Origin", origin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestExtensionRejectsBadRequests(t *testing.T) {
	isolateUserCache(t)
	handler := extensionHandler(func() []Account { return testAccounts }, nil)
	valid := extensionBody(t)
	encode := func(body map[string]any) string {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	validJSON := encode(valid)
	valid["token"] = "x"
	unknownJSON := encode(valid)
	delete(valid, "token")
	// Organization is an optional matching hint with no length restriction.
	valid["link"].(map[string]any)["organization"] = strings.Repeat("x", maxBodyBytes)
	largeJSON := encode(valid)
	if got := extensionRequest(handler, "POST", "/v1/api-credits", extensionAddress, ExtensionOrigins[0], validJSON); got.Code != 204 {
		t.Fatalf("valid control = %d: %s", got.Code, got.Body.String())
	}
	for _, tc := range []struct {
		name, method, host, origin, body string
		want                             int
	}{
		{"origin", "POST", extensionAddress, consoleOrigin, "{}", 403},
		{"missing origin", "POST", extensionAddress, "", "{}", 403},
		{"host", "POST", "localhost:17344", ExtensionOrigins[0], "{}", 403},
		{"unknown", "POST", extensionAddress, ExtensionOrigins[0], unknownJSON, 400},
		{"missing fields", "POST", extensionAddress, ExtensionOrigins[0], "{}", 400},
		{"nonce", "POST", extensionAddress, ExtensionOrigins[0], creditsBody(t, testNonce), 400},
		{"trailing", "POST", extensionAddress, ExtensionOrigins[0], validJSON + validJSON, 400},
		{"large", "POST", extensionAddress, ExtensionOrigins[0], largeJSON, 400},
		{"method", "GET", extensionAddress, ExtensionOrigins[0], "", 405},
		{"preflight", "OPTIONS", extensionAddress, ExtensionOrigins[0], "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := extensionRequest(handler, tc.method, "/v1/api-credits", tc.host, tc.origin, tc.body)
			if got.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", got.Code, tc.want, got.Body.String())
			}
		})
	}
}

func TestExtensionSavesAndLinksPrivately(t *testing.T) {
	isolateUserCache(t)
	saves := 0
	handler := extensionHandler(func() []Account { return testAccounts }, func(Result) { saves++ })
	body, _ := json.Marshal(extensionBody(t))
	post := func() APICreditPool {
		t.Helper()
		got := extensionRequest(handler, "POST", "/v1/api-credits", extensionAddress, ExtensionOrigins[0], string(body))
		if got.Code != 204 {
			t.Fatalf("save = %d: %s", got.Code, got.Body.String())
		}
		pools, err := ReadAPICreditPools()
		if err != nil || len(pools) != 1 {
			t.Fatalf("pools = %+v, %v", pools, err)
		}
		return pools[0]
	}
	first, second := post(), post()
	if saves != 2 || second.Source != "extension" || second.LinkedTarget != "claude:odl" ||
		!second.LinkedTo("org-odl") || second.LinkedTo("org-default") || first.LinkSalt == second.LinkSalt ||
		!second.ObservedAt.Equal(first.ObservedAt) {
		t.Fatalf("saved pools = %+v, %+v; callbacks %d", first, second, saves)
	}
	dir, _ := stateDir()
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	for _, path := range files {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "org-odl") || strings.Contains(string(raw), "org-api") {
			t.Fatal("raw organization persisted")
		}
	}
}

func TestExtensionAmbiguousOrMissingLinkSavesUnlinked(t *testing.T) {
	for _, accounts := range [][]Account{
		nil,
		{{Key: "claude", OrgUUID: "org-odl"}, {Key: "claude:other", OrgUUID: "org-odl"}},
		{{Key: "invalid/key", OrgUUID: "org-odl"}},
	} {
		isolateUserCache(t)
		handler := extensionHandler(func() []Account { return accounts }, nil)
		raw, _ := json.Marshal(extensionBody(t))
		got := extensionRequest(handler, "POST", "/v1/api-credits", extensionAddress, ExtensionOrigins[0], string(raw))
		pools, _ := ReadAPICreditPools()
		if got.Code != 204 || len(pools) != 1 || pools[0].LinkedTarget != "" || pools[0].LinkSalt != "" || pools[0].LinkHash != "" {
			t.Fatalf("unlinked save = %d, %+v", got.Code, pools)
		}
	}
}

func TestExtensionSignOutRoundTrip(t *testing.T) {
	isolateUserCache(t)
	saves := 0
	handler := extensionHandler(nil, func(Result) { saves++ })
	if state, err := ReadExtensionState(); err != nil || state.Status != "" {
		t.Fatalf("missing state = %+v, %v", state, err)
	}
	for _, body := range []string{`{"status":"other"}`, `{"status":"signed_out","extra":1}`, `{"status":"signed_out"}{}`} {
		if got := extensionRequest(handler, "POST", "/v1/status", extensionAddress, ExtensionOrigins[0], body); got.Code != 400 {
			t.Fatalf("bad status accepted: %d", got.Code)
		}
	}
	got := extensionRequest(handler, "POST", "/v1/status", extensionAddress, ExtensionOrigins[0], `{"status":"signed_out"}`)
	state, err := ReadExtensionState()
	if got.Code != 204 || err != nil || state.Status != "signed_out" || state.At.IsZero() || saves != 1 {
		t.Fatalf("state = %+v, %v, response %d", state, err, got.Code)
	}
	raw, _ := json.Marshal(extensionBody(t))
	got = extensionRequest(handler, "POST", "/v1/api-credits", extensionAddress, ExtensionOrigins[0], string(raw))
	state, err = ReadExtensionState()
	if got.Code != 204 || err != nil || state.Status != "signed_out" || saves != 2 {
		t.Fatalf("retained state = %+v, %v, response %d", state, err, got.Code)
	}
}

func TestExtensionObservationTime(t *testing.T) {
	isolateUserCache(t)
	handler := extensionHandler(nil, nil)
	for _, tc := range []struct {
		name     string
		observed string
		want     int
	}{
		{"missing", "", 400},
		{"invalid", "yesterday", 400},
		{"past", time.Now().Add(-5*time.Minute - time.Second).UTC().Format(time.RFC3339Nano), 400},
		{"future", time.Now().Add(time.Minute + 10*time.Second).UTC().Format(time.RFC3339Nano), 400},
		{"valid", time.Now().Add(-4 * time.Minute).UTC().Format(time.RFC3339Nano), 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := extensionBody(t)
			delete(body, "observed_at")
			if tc.observed != "" {
				body["observed_at"] = tc.observed
			}
			raw, _ := json.Marshal(body)
			got := extensionRequest(handler, "POST", "/v1/api-credits", extensionAddress, ExtensionOrigins[0], string(raw))
			if got.Code != tc.want {
				t.Fatalf("status = %d: %s", got.Code, got.Body.String())
			}
			if tc.want == 204 {
				pools, _ := ReadAPICreditPools()
				observed, _ := time.Parse(time.RFC3339Nano, tc.observed)
				if len(pools) != 1 || !pools[0].ObservedAt.Equal(observed) {
					t.Fatalf("observation changed: %+v", pools)
				}
			}
		})
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, delta := range []time.Duration{-5 * time.Minute, time.Minute} {
		if _, err := extensionObservedAt(now.Add(delta).Format(time.RFC3339Nano), now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtensionSavesResetsForATrackedOrganization(t *testing.T) {
	isolateUserCache(t)
	var results []Result
	handler := extensionHandler(func() []Account { return testAccounts }, func(result Result) { results = append(results, result) })
	observed := time.Now().Add(-time.Minute).UTC()
	ends := observed.Add(10 * 24 * time.Hour).Format(time.RFC3339)
	body := func(org string) string {
		raw, err := json.Marshal(map[string]any{
			"observed_at": observed.Format(time.RFC3339Nano), "organization": org,
			"grants": []map[string]any{
				{"resets_left": 2, "starts_at": "2026-01-01T00:00:00Z", "ends_at": ends, "paused": false},
				{"resets_left": 1, "starts_at": "2026-01-01T00:00:00Z", "ends_at": ends, "paused": true},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if got := extensionRequest(handler, "POST", "/v1/resets", extensionAddress, ExtensionOrigins[0], body("org-odl")); got.Code != 204 {
		t.Fatalf("resets = %d: %s", got.Code, got.Body.String())
	}
	summary, err := ReadSummaryFor("claude:odl")
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Credits) != 2 || !summary.ObservedAt.Equal(observed) || summary.MatchHash != AccountHash(summary.MatchSalt, "org-odl") {
		t.Fatalf("summary = %+v", summary)
	}
	if len(results) != 1 || !results[0].IsResets() || results[0].Resets.ResetCreditsTarget != "claude:odl" {
		t.Fatalf("results = %+v", results)
	}

	// An organization Clawmeter doesn't track is accepted and dropped.
	if got := extensionRequest(handler, "POST", "/v1/resets", extensionAddress, ExtensionOrigins[0], body("org-other")); got.Code != 204 || len(results) != 1 {
		t.Fatalf("untracked = %d, results %d", got.Code, len(results))
	}
	for name, bad := range map[string]string{
		"no grants":  `{"observed_at":"` + observed.Format(time.RFC3339Nano) + `","organization":"org-odl"}`,
		"stale":      strings.Replace(body("org-odl"), observed.Format(time.RFC3339Nano), observed.Add(-time.Hour).Format(time.RFC3339Nano), 1),
		"bad grant":  strings.Replace(body("org-odl"), `"paused":false`, `"paused":null`, 1),
		"extra":      strings.Replace(body("org-odl"), `{"grants"`, `{"nonce":"x","grants"`, 1),
		"no account": strings.Replace(body("org-odl"), `"organization":"org-odl"`, `"organization":""`, 1),
	} {
		if got := extensionRequest(handler, "POST", "/v1/resets", extensionAddress, ExtensionOrigins[0], bad); got.Code != 400 {
			t.Errorf("%s = %d", name, got.Code)
		}
	}
	if len(results) != 1 {
		t.Fatalf("rejected readings were reported: %d", len(results))
	}
}
