package claudeweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func extensionBody(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(creditsBody(t, testNonce)), &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "nonce")
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
	for _, tc := range []struct {
		name, method, host, origin, body string
		want                             int
	}{
		{"origin", "POST", extensionAddress, consoleOrigin, "{}", 403},
		{"missing origin", "POST", extensionAddress, "", "{}", 403},
		{"host", "POST", "localhost:17344", ExtensionOrigins[0], "{}", 403},
		{"unknown", "POST", extensionAddress, ExtensionOrigins[0], `{"token":"x"}`, 400},
		{"missing fields", "POST", extensionAddress, ExtensionOrigins[0], "{}", 400},
		{"nonce", "POST", extensionAddress, ExtensionOrigins[0], creditsBody(t, testNonce), 400},
		{"trailing", "POST", extensionAddress, ExtensionOrigins[0], "{} {}", 400},
		{"large", "POST", extensionAddress, ExtensionOrigins[0], strings.Repeat(" ", maxBodyBytes+1), 400},
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
	handler := extensionHandler(func() []Account { return testAccounts }, func() { saves++ })
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
		!second.ObservedAt.After(first.ObservedAt) {
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
	handler := extensionHandler(nil, func() { saves++ })
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
	if got.Code != 204 || err != nil || state.Status != "" || saves != 2 {
		t.Fatalf("clear state = %+v, %v, response %d", state, err, got.Code)
	}
}
