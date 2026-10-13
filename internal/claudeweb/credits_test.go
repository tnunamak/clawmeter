package claudeweb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func poolHash(org string) string {
	sum := sha256.Sum256([]byte("clawmeter-api-pool\x00" + org))
	return hex.EncodeToString(sum[:])
}

// extensionBody is the payload the extension sends for the live 2026-10-08
// Console snapshot.
func extensionBody(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"observed_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano),
		"pool":        poolHash("org-api"), "name": "Family's Individual Organization", "currency": "USD",
		"balance": 19988, "month_spend": 11, "month_resets_at": "2026-11-01T00:00:00Z",
		"grants": []map[string]any{{"name": "API credit (Max 20x)", "granted": 20000, "remaining": 19988, "granted_at": "2026-10-08T02:31:09.349000Z", "expires_at": "2026-10-25T00:00:00Z"}},
		"daily":  map[string]float64{"2026-10-08": 11.2965},
		"link":   map[string]any{"organization": "org-odl", "plan": "max_20x", "monthly_credit": 20000},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestExtensionRejectsUnreadableCredits(t *testing.T) {
	isolateUserCache(t)
	handler := ExtensionHandler(func() []Account { return testAccounts }, nil)
	for name, mutate := range map[string]func(map[string]any){
		"balance out of range": func(body map[string]any) { body["balance"] = -100000000001 },
		"bad currency":         func(body map[string]any) { body["currency"] = "dollars" },
		"remaining above granted": func(body map[string]any) {
			body["grants"].([]any)[0].(map[string]any)["remaining"] = 30000
		},
		"bad day":       func(body map[string]any) { body["daily"] = map[string]any{"Oct 8": 1} },
		"bad pool":      func(body map[string]any) { body["pool"] = "org-api" },
		"unknown field": func(body map[string]any) { body["session_key"] = "x" },
	} {
		body := extensionBody(t)
		mutate(body)
		raw, _ := json.Marshal(body)
		if got := extensionRequest(handler, "POST", "/v1/api-credits", extensionAddress, ExtensionOrigins[0], string(raw)); got.Code != http.StatusBadRequest {
			t.Errorf("%s: credits = %d %q, want 400", name, got.Code, got.Body.String())
		}
	}
	if pools, _ := ReadAPICreditPools(); len(pools) != 0 {
		t.Fatalf("saved %d pools from bad data", len(pools))
	}
}
