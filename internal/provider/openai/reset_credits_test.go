package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseResetCreditsFiltersAndOrdersAvailableCredits(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	body := []byte(`{
		"available_count": 4,
		"credits": [
			{"status":"available","created_at":"2026-07-01T12:00:00Z","expires_at":"2026-07-06T12:00:00Z"},
			{"status":"available","created_at":"2026-07-02T12:00:00Z","expires_at":"2026-07-04T12:00:00Z"},
			{"status":"available","expires_at":"2026-07-05T12:00:00Z","consumed_at":"2026-07-03T11:00:00Z"},
			{"status":"expired","expires_at":"2026-07-04T12:00:00Z"},
			{"status":"available","expires_at":"2026-07-02T12:00:00Z"},
			{"status":"available","expires_at":"not-a-time"}
		]
	}`)

	got, err := parseResetCredits(body, now)
	if err != nil {
		t.Fatalf("parseResetCredits() error = %v", err)
	}
	if got == nil {
		t.Fatal("parseResetCredits() = nil, want summary")
	}
	if count := got.DisplayCount(now); count != 4 {
		t.Fatalf("DisplayCount() = %d, want first-party count 4", count)
	}
	if len(got.Credits) != 3 {
		t.Fatalf("len(Credits) = %d, want 3 available unconsumed entries", len(got.Credits))
	}
	if got.Credits[0].ExpiresAt.Format(time.RFC3339) != "2026-07-04T12:00:00Z" {
		t.Fatalf("first expiry = %s, want earliest valid expiry first", got.Credits[0].ExpiresAt.Format(time.RFC3339))
	}
	if got.Credits[2].ExpiresAt.IsZero() != true {
		t.Fatalf("invalid expiry should be retained only as unknown expiry")
	}
	if got.Warning == "" {
		t.Fatal("Warning is empty, want count/detail disagreement warning")
	}
}

func TestParseResetCreditsReturnsNilWhenNoAvailableCredits(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	got, err := parseResetCredits([]byte(`{"available_count":0,"credits":[{"status":"consumed"}]}`), now)
	if err != nil {
		t.Fatalf("parseResetCredits() error = %v", err)
	}
	if got != nil {
		t.Fatalf("parseResetCredits() = %+v, want nil for no available credits", got)
	}
}

func TestFetchResetCreditsUsesReadOnlyEndpointAndHeaders(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if strings.Contains(r.URL.Path, "/consume") {
			t.Fatalf("fetchResetCredits called consume path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("Authorization header is empty")
		}
		if r.Header.Get("ChatGPT-Account-ID") == "" {
			t.Fatal("ChatGPT-Account-ID header is empty")
		}
		if r.Header.Get("Originator") != "Codex Desktop" {
			t.Fatalf("Originator = %q, want Codex Desktop", r.Header.Get("Originator"))
		}
		if r.Header.Get("OAI-Product-Sku") != "CODEX" {
			t.Fatalf("OAI-Product-Sku = %q, want CODEX", r.Header.Get("OAI-Product-Sku"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"available_count":1,"credits":[{"status":"available","expires_at":"2099-08-01T12:00:00Z"}]}`))
	}))
	defer server.Close()

	restore := replaceResetCreditTransport(server.URL+resetCreditsPath, server.Client())
	defer restore()

	got, err := fetchResetCredits(context.Background(), testAuth("fake-access-token", "acct_fake"))
	if err != nil {
		t.Fatalf("fetchResetCredits() error = %v", err)
	}
	if got == nil || got.DisplayCount(time.Now()) != 1 {
		t.Fatalf("fetchResetCredits() = %+v, want one available reset", got)
	}
	if gotPath != resetCreditsPath {
		t.Fatalf("path = %q, want %q", gotPath, resetCreditsPath)
	}
}

func TestFetchResetCreditsSkipsMissingChatGPTAuth(t *testing.T) {
	restore := replaceResetCreditTransport("http://127.0.0.1:1"+resetCreditsPath, http.DefaultClient)
	defer restore()

	got, err := fetchResetCredits(context.Background(), &authFile{OpenAIAPIKey: "sk-fake"})
	if err != nil {
		t.Fatalf("fetchResetCredits() error = %v", err)
	}
	if got != nil {
		t.Fatalf("fetchResetCredits() = %+v, want nil for API-key auth", got)
	}
}

func TestFetchResetCreditsRefusesConsumeURL(t *testing.T) {
	restore := replaceResetCreditTransport("https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume", http.DefaultClient)
	defer restore()

	_, err := fetchResetCredits(context.Background(), testAuth("fake-access-token", "acct_fake"))
	if err == nil || !strings.Contains(err.Error(), "consume") {
		t.Fatalf("fetchResetCredits() error = %v, want consume refusal", err)
	}
}

func TestFetchResetCreditsNon2XXDoesNotExposeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "sensitive body", http.StatusTooManyRequests)
	}))
	defer server.Close()

	restore := replaceResetCreditTransport(server.URL+resetCreditsPath, server.Client())
	defer restore()

	_, err := fetchResetCredits(context.Background(), testAuth("fake-access-token", "acct_fake"))
	if err == nil {
		t.Fatal("fetchResetCredits() error = nil, want non-2xx error")
	}
	if strings.Contains(err.Error(), "sensitive body") {
		t.Fatalf("error leaked response body: %v", err)
	}
}

func testAuth(accessToken, accountID string) *authFile {
	return &authFile{Tokens: &struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	}{
		AccessToken: accessToken,
		AccountID:   accountID,
	}}
}

func replaceResetCreditTransport(url string, client *http.Client) func() {
	oldURL := resetCreditsURL
	oldClient := resetCreditsHTTPClient
	resetCreditsURL = url
	resetCreditsHTTPClient = client
	return func() {
		resetCreditsURL = oldURL
		resetCreditsHTTPClient = oldClient
	}
}
