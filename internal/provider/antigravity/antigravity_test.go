package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestFetchUsageReturnsAuthoritativeWeeklyPools(t *testing.T) {
	home := t.TempDir()
	writeToken(t, home, "test-token", time.Now().Add(time.Hour))

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			var body struct {
				Metadata struct {
					IDEType string `json:"ideType"`
				} `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Metadata.IDEType != "ANTIGRAVITY" {
				t.Fatalf("ideType = %q", body.Metadata.IDEType)
			}
			_, _ = w.Write([]byte(`{"cloudaicompanionProject":"account-project"}`))
		case "/v1internal:retrieveUserQuotaSummary":
			var body struct {
				Project string `json:"project"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Project != "account-project" {
				t.Fatalf("project = %q", body.Project)
			}
			_, _ = w.Write([]byte(`{
				"groups": [
					{"displayName":"Gemini Models","buckets":[
						{"bucketId":"gemini-weekly","displayName":"Weekly Limit","remainingFraction":0.75,"resetTime":"2026-07-30T17:47:46Z","window":"weekly"}
					]},
					{"displayName":"Claude and GPT models","buckets":[
						{"bucketId":"3p-weekly","displayName":"Weekly Limit","remaining":{"remainingFraction":0.4},"resetTime":"2026-07-30T17:47:46Z","window":"weekly"}
					]}
				]
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	p := newTestProvider(home, server.URL)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths, ","); got != "/v1internal:loadCodeAssist,/v1internal:retrieveUserQuotaSummary" {
		t.Fatalf("paths = %q", got)
	}
	if len(data.Windows) != 2 {
		t.Fatalf("windows = %+v", data.Windows)
	}
	assertWindow(t, data.Windows[0], "7d Gemini", "7 days (Gemini)", 25)
	assertWindow(t, data.Windows[1], "7d Claude + GPT", "7 days (Claude + GPT)", 60)
}

func TestFetchUsageRefreshesExpiredTokenWithoutWritingLoginFile(t *testing.T) {
	home := t.TempDir()
	writeToken(t, home, "expired", time.Now().Add(-time.Hour))
	tokenPath := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	before, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(home, "agy")
	if err := os.WriteFile(binaryPath, testOAuthBinary(), 0o700); err != nil {
		t.Fatal(err)
	}

	refreshes := 0
	p := newTestProvider(home, "http://unused.invalid")
	p.tokenURL = "https://oauth.test/token"
	p.lookPath = func(string) (string, error) { return binaryPath, nil }
	p.client = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == p.tokenURL {
			refreshes++
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Form.Get("client_id") == "884354919052-business.apps.googleusercontent.com" {
				return jsonStatusResponse(http.StatusBadRequest, `{"error":"invalid_client"}`), nil
			}
			if req.Form.Get("client_id") != "1071006060591-cli.apps.googleusercontent.com" ||
				req.Form.Get("client_secret") != testOAuthSecret() ||
				req.Form.Get("refresh_token") != "test-refresh" {
				t.Fatal("refresh request did not use the installed agy OAuth client")
			}
			return jsonResponse(`{"access_token":"fresh","expires_in":3600}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer fresh" {
			t.Fatalf("Authorization = %q, want refreshed token", req.Header.Get("Authorization"))
		}
		body := `{"cloudaicompanionProject":"p"}`
		if strings.Contains(req.URL.Path, "retrieveUserQuotaSummary") {
			body = `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","remainingFraction":1,"resetTime":"2026-07-30T17:47:46Z"}]}]}`
		}
		return jsonResponse(body), nil
	})

	if _, err := p.FetchUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FetchUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshes != 2 {
		t.Fatalf("OAuth refreshes = %d, want two candidate attempts and then in-memory reuse", refreshes)
	}
	after, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("Clawmeter rewrote the agy credential file")
	}
}

func TestParseOAuthClientsUsesExactSecretBoundary(t *testing.T) {
	clients, err := parseOAuthClients(testOAuthBinary())
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients = %+v", clients)
	}
	for _, client := range clients {
		if client.Secret != testOAuthSecret() {
			t.Fatalf("client secret length = %d, want %d", len(client.Secret), oauthSecretLength)
		}
	}
}

func TestExpiredTokenFailsSoftWhenRefreshResponseIsInvalid(t *testing.T) {
	home := t.TempDir()
	writeToken(t, home, "expired", time.Now().Add(-time.Hour))
	binaryPath := filepath.Join(home, "agy")
	if err := os.WriteFile(binaryPath, testOAuthBinary(), 0o700); err != nil {
		t.Fatal(err)
	}
	p := newTestProvider(home, "http://unused.invalid")
	p.lookPath = func(string) (string, error) { return binaryPath, nil }
	p.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"expires_in":3600}`), nil
	})

	_, err := p.FetchUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid login refresh") {
		t.Fatalf("error = %v", err)
	}
}

func TestOAuthServiceFailureDoesNotTryEveryClient(t *testing.T) {
	home := t.TempDir()
	writeToken(t, home, "expired", time.Now().Add(-time.Hour))
	binaryPath := filepath.Join(home, "agy")
	if err := os.WriteFile(binaryPath, testOAuthBinary(), 0o700); err != nil {
		t.Fatal(err)
	}
	p := newTestProvider(home, "http://unused.invalid")
	p.lookPath = func(string) (string, error) { return binaryPath, nil }
	requests := 0
	p.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return jsonStatusResponse(http.StatusServiceUnavailable, `{}`), nil
	})

	_, err := p.FetchUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestFetchUsageFailsInsteadOfInventingZero(t *testing.T) {
	tests := map[string]string{
		"missing groups":     `{}`,
		"missing fraction":   `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","resetTime":"2026-07-30T17:47:46Z"}]}]}`,
		"invalid fraction":   `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","remainingFraction":1.2,"resetTime":"2026-07-30T17:47:46Z"}]}]}`,
		"missing reset time": `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","remainingFraction":0.5}]}]}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseQuotaSummary([]byte(payload), time.Now())
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseQuotaSummarySkipsDisabledAndRejectsConflictingDuplicates(t *testing.T) {
	payload := []byte(`{"groups":[
		{"displayName":"Gemini Models","buckets":[
			{"bucketId":"disabled","remainingFraction":0,"resetTime":"2026-07-30T17:47:46Z","disabled":true},
			{"bucketId":"gemini-weekly","remainingFraction":0.8,"resetTime":"2026-07-30T17:47:46Z"},
			{"bucketId":"gemini-weekly","remainingFraction":0.7,"resetTime":"2026-07-30T17:47:46Z"}
		]}
	]}`)
	if _, err := parseQuotaSummary(payload, time.Now()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderNeverUsesMutationOrConsumeEndpoint(t *testing.T) {
	for _, path := range []string{loadCodeAssistPath, quotaSummaryPath, defaultTokenURL} {
		lower := strings.ToLower(path)
		if strings.Contains(lower, "consume") || strings.Contains(lower, "redeem") || strings.Contains(lower, "set") {
			t.Fatalf("provider endpoint is not read-only: %s", path)
		}
	}
}

func TestUnauthorizedInvalidatesCachedQuota(t *testing.T) {
	home := t.TempDir()
	writeToken(t, home, "test-token", time.Now().Add(time.Hour))
	p := newTestProvider(home, "http://unused.invalid")
	p.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       ioNopCloser{strings.NewReader(`{}`)},
			Header:     make(http.Header),
		}, nil
	})

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !data.IsExpired || !data.InvalidatesPriorUsage || len(data.Windows) != 0 {
		t.Fatalf("unauthorized data = %+v", data)
	}
}

func TestSetupStatusRequiresCLIAndUsableToken(t *testing.T) {
	home := t.TempDir()
	p := newTestProvider(home, "http://unused.invalid")
	p.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if got := p.SetupStatus(); got.State != provider.SetupUnavailable {
		t.Fatalf("without CLI: %+v", got)
	}

	p.lookPath = func(string) (string, error) { return "/bin/agy", nil }
	if got := p.SetupStatus(); got.State != provider.SetupNeedsAuth {
		t.Fatalf("without token: %+v", got)
	}

	writeToken(t, home, "token", time.Now().Add(time.Hour))
	if got := p.SetupStatus(); got.State != provider.SetupReady {
		t.Fatalf("with token: %+v", got)
	}
}

func TestSetupStatusExplainsUnsafeOrMalformedLoginFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	home := t.TempDir()
	p := newTestProvider(home, "http://unused.invalid")
	writeToken(t, home, "token", time.Now().Add(time.Hour))
	path := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.SetupStatus(); got.State != provider.SetupNeedsAuth || !strings.Contains(got.Detail, "only to its owner") {
		t.Fatalf("unsafe permissions status = %+v", got)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.SetupStatus(); got.State != provider.SetupNeedsAuth || !strings.Contains(got.Detail, "unreadable") {
		t.Fatalf("malformed login status = %+v", got)
	}
}

func newTestProvider(home, baseURL string) *Provider {
	p := New()
	p.homeDir = func() (string, error) { return home, nil }
	p.baseURL = baseURL
	p.lookPath = func(string) (string, error) { return "/bin/agy", nil }
	return p
}

func writeToken(t *testing.T, home, accessToken string, expiry time.Time) {
	t.Helper()
	path := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"auth_method": "consumer",
		"token": map[string]any{
			"access_token":  accessToken,
			"refresh_token": "test-refresh",
			"expiry":        expiry.Format(time.RFC3339Nano),
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testOAuthBinary() []byte {
	return []byte(
		"\x00" + testOAuthSecret() + "adjacent-packed-data\x00" +
			"884354919052-business.apps.googleusercontent.com\x00" +
			"1071006060591-cli.apps.googleusercontent.com\x00")
}

func testOAuthSecret() string {
	return "GOCSPX-" + strings.Repeat("x", oauthSecretLength-len("GOCSPX-"))
}

func assertWindow(t *testing.T, got provider.UsageWindow, name, display string, utilization float64) {
	t.Helper()
	if got.Name != name || got.DisplayName != display || got.Utilization != utilization || got.ResetsAt.IsZero() {
		t.Fatalf("window = %+v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(body string) *http.Response {
	return jsonStatusResponse(http.StatusOK, body)
}

func jsonStatusResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       ioNopCloser{strings.NewReader(body)},
		Header:     make(http.Header),
	}
}

type ioNopCloser struct{ *strings.Reader }

func (ioNopCloser) Close() error { return nil }
