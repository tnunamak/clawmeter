package claudeweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func poolHash(org string) string {
	sum := sha256.Sum256([]byte("clawmeter-api-pool\x00" + org))
	return hex.EncodeToString(sum[:])
}

// creditsBody is the payload the bookmark sends for the live 2026-10-08
// Console snapshot.
func creditsBody(t *testing.T, nonce string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"nonce": nonce, "pool": poolHash("org-api"), "name": "Family's Individual Organization", "currency": "USD",
		"balance": 19988, "month_spend": 11, "month_resets_at": "2026-11-01T00:00:00Z",
		"grants": []map[string]any{{"name": "API credit (Max 20x)", "granted": 20000, "remaining": 19988, "granted_at": "2026-10-08T02:31:09.349000Z", "expires_at": "2026-10-25T00:00:00Z"}},
		"daily":  map[string]float64{"2026-10-08": 11.2965},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCreditsCheckSavesTheConsoleSnapshot(t *testing.T) {
	isolateUserCache(t)
	s := newSession(KindAPICredits, testNonce, testAccounts)
	response := serve(t, s, http.MethodPost, "/credits", consoleOrigin, creditsBody(t, testNonce))
	if response.Code != http.StatusOK || message(t, response) != "Saved API credits for Family's Individual Organization." {
		t.Fatalf("credits = %d %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != consoleOrigin {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	result, err := s.Wait(context.Background())
	if err != nil || result.APICredits.Balance != 19988 {
		t.Fatalf("Wait() = %+v, %v", result, err)
	}
	pools, err := ReadAPICreditPools()
	if err != nil || len(pools) != 1 {
		t.Fatalf("pools = %+v, %v", pools, err)
	}
	pool := pools[0]
	if pool.Pool != poolHash("org-api") || pool.MonthSpend != 11 || len(pool.Grants) != 1 || pool.Grants[0].Remaining != 19988 || pool.DailySpend["2026-10-08"] != 11.2965 {
		t.Fatalf("saved pool = %+v", pool)
	}
	files, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "clawmeter", "*"))
	for _, file := range files {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), "org-api") {
			t.Fatalf("%s stores the raw organization ID", file)
		}
	}
}

// A Console organization funded by a Claude plan is filed under the local
// Claude source signed in to that plan, proven the same way as resets.
func TestCreditsCheckLinksThePoolToItsClaudePlan(t *testing.T) {
	isolateUserCache(t)
	for _, tc := range []struct {
		planOrg, wantTarget, wantMessage string
	}{
		{"org-odl", "claude:odl", "Saved API credits for ODL."},
		{"org-elsewhere", "", "Saved API credits for Family's Individual Organization."},
	} {
		var body map[string]any
		if err := json.Unmarshal([]byte(creditsBody(t, testNonce)), &body); err != nil {
			t.Fatal(err)
		}
		body["link"] = map[string]any{"account": AccountHash(testNonce, tc.planOrg), "plan": "max_20x", "monthly_credit": 20000}
		raw, _ := json.Marshal(body)
		s := newSession(KindAPICredits, testNonce, testAccounts)
		response := serve(t, s, http.MethodPost, "/credits", consoleOrigin, string(raw))
		if response.Code != http.StatusOK || message(t, response) != tc.wantMessage {
			t.Fatalf("%s: credits = %d %q", tc.planOrg, response.Code, response.Body.String())
		}
		pools, _ := ReadAPICreditPools()
		pool := pools[0]
		if pool.LinkedTarget != tc.wantTarget || pool.Plan != "max_20x" || pool.MonthlyCredit != 20000 {
			t.Fatalf("%s: pool = %+v", tc.planOrg, pool)
		}
		if tc.wantTarget != "" && (!pool.LinkedTo("org-odl") || pool.LinkedTo("org-default")) {
			t.Fatalf("%s: link not bound to org-odl: %+v", tc.planOrg, pool)
		}
	}
}

func TestEachCheckOnlyTakesItsOwnKind(t *testing.T) {
	isolateUserCache(t)
	resets := newSession(KindResets, testNonce, testAccounts)
	if got := serve(t, resets, http.MethodPost, "/credits", consoleOrigin, creditsBody(t, testNonce)); got.Code != http.StatusConflict || message(t, got) != msgWantUsage {
		t.Fatalf("credits during a reset check = %d %q", got.Code, got.Body.String())
	}
	credits := newSession(KindAPICredits, testNonce, testAccounts)
	if got := serve(t, credits, http.MethodPost, "/result", claudeOrigin, resultBody(t, testNonce, "org-odl")); got.Code != http.StatusConflict || message(t, got) != msgWantConsole {
		t.Fatalf("resets during a credits check = %d %q", got.Code, got.Body.String())
	}
	if got := serve(t, credits, http.MethodPost, "/credits", claudeOrigin, creditsBody(t, testNonce)); got.Code != http.StatusForbidden {
		t.Fatalf("credits from claude.ai = %d, want 403", got.Code)
	}
	if got := serve(t, credits, http.MethodGet, "/challenge", consoleOrigin, ""); got.Code != http.StatusOK {
		t.Fatalf("challenge from the Console = %d", got.Code)
	}
	if pools, _ := ReadAPICreditPools(); len(pools) != 0 {
		t.Fatalf("saved %d pools", len(pools))
	}
}

func TestCreditsCheckRejectsUnreadableData(t *testing.T) {
	isolateUserCache(t)
	for name, mutate := range map[string]func(map[string]any){
		"negative balance": func(body map[string]any) { body["balance"] = -1 },
		"bad currency":     func(body map[string]any) { body["currency"] = "dollars" },
		"remaining above granted": func(body map[string]any) {
			body["grants"].([]any)[0].(map[string]any)["remaining"] = 30000
		},
		"bad day":       func(body map[string]any) { body["daily"] = map[string]any{"Oct 8": 1} },
		"bad pool":      func(body map[string]any) { body["pool"] = "org-api" },
		"unknown field": func(body map[string]any) { body["session_key"] = "x" },
	} {
		var body map[string]any
		if err := json.Unmarshal([]byte(creditsBody(t, testNonce)), &body); err != nil {
			t.Fatal(err)
		}
		mutate(body)
		raw, _ := json.Marshal(body)
		s := newSession(KindAPICredits, testNonce, testAccounts)
		if got := serve(t, s, http.MethodPost, "/credits", consoleOrigin, string(raw)); got.Code != http.StatusBadRequest {
			t.Errorf("%s: credits = %d %q, want 400", name, got.Code, got.Body.String())
		}
	}
	if pools, _ := ReadAPICreditPools(); len(pools) != 0 {
		t.Fatalf("saved %d pools from bad data", len(pools))
	}
}

// TestBookmarkletReadsTheConsole runs the real bookmarklet against a fake
// Claude Console shaped like the live 2026-10-08 responses, then checks the
// Go handler accepts exactly what it posts.
func TestBookmarkletReadsTheConsole(t *testing.T) {
	run := runConsoleBookmarklet(t, `{ costs: { [today]: [row(8.6137), row(2.68275)] }, web_search_costs: {}, code_execution_costs: {}, session_usage_costs: {}, claude_code_savings: { [today]: [row(500)] } }`)
	if len(run.Alerts) != 1 || run.Alerts[0] != "Clawmeter: Saved API credits for Family's Individual Organization." || len(run.Posted) != 1 {
		t.Fatalf("run = %+v", run)
	}
	if strings.Contains(run.Posted[0], "org-api") || strings.Contains(run.Posted[0], "org-odl") {
		t.Fatalf("bookmarklet posted a raw organization ID: %s", run.Posted[0])
	}
	isolateUserCache(t)
	s := newSession(KindAPICredits, testNonce, testAccounts)
	if response := serve(t, s, http.MethodPost, "/credits", consoleOrigin, run.Posted[0]); response.Code != http.StatusOK {
		t.Fatalf("Go handler rejected the real bookmarklet payload: %d %q (%s)", response.Code, response.Body.String(), run.Posted[0])
	}
	pools, _ := ReadAPICreditPools()
	// Savings are not spend: only the 11.30¢ of costs is counted.
	var spent float64
	for _, amount := range pools[0].DailySpend {
		spent += amount
	}
	// The balance is what the Console shows: purchased plus promotional.
	if len(pools) != 1 || pools[0].Balance != 19988 || pools[0].Pool != poolHash("org-api") || spent < 11.29 || spent > 11.30 || pools[0].Grants[0].ExpiresAt.IsZero() ||
		pools[0].LinkedTarget != "claude:odl" || pools[0].Plan != "max_20x" {
		t.Fatalf("pools = %+v", pools)
	}
}

// A cost report without its costs map is not zero spend: nothing is sent.
func TestBookmarkletRejectsACostReportWithoutCosts(t *testing.T) {
	run := runConsoleBookmarklet(t, `{}`)
	if len(run.Posted) != 0 || len(run.Alerts) != 1 || run.Alerts[0] != "Clawmeter: Claude Console's credit data changed format. Nothing was sent." {
		t.Fatalf("run = %+v", run)
	}
}

type bookmarkletRun struct{ Posted, Alerts []string }

// runConsoleBookmarklet runs the real bookmarklet against a fake Claude
// Console shaped like the live 2026-10-08 responses. defaultCosts is the
// default workspace's usage_cost body, as JavaScript.
func runConsoleBookmarklet(t *testing.T, defaultCosts string) bookmarkletRun {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	harness := `
const posted = [], alerts = [];
globalThis.location = { origin: "https://platform.claude.com" };
globalThis.document = { cookie: "lastActiveOrg=org-api" };
globalThis.window = {};
globalThis.alert = message => alerts.push(message);
const json = (status, body) => ({ ok: status < 300, status, json: async () => body });
const today = new Date().toISOString().slice(0, 10);
const row = total => ({ workspace_id: "default", key_id: "apikey_x", key_name: "k", model_name: "Claude Sonnet 5.5", total, token_type: "output", usage_type: "message" });
globalThis.fetch = async (url, options = {}) => {
  if (url === "/api/organizations") return json(200, [{ uuid: "org-chat", name: "Chat", capabilities: ["claude_max", "chat"] }, { uuid: "org-api", name: "Family's Individual Organization", capabilities: ["api", "api_individual"] }]);
  if (url === "/api/organizations/org-api/prepaid/credits") return json(200, { amount: 19989, amount_without_scoped_credits: 0, currency: "USD", balance: { money: null, credits: { amount_minor: 19989, exponent: 2 } }, tranches: [], promo_tranches: [
    { remaining_amount_minor_units: 19988, currency: "USD", expires_at: "2026-10-25T00:00:00Z", granted_amount_minor_units: 20000, granted_at: "2026-10-08T02:31:09.349000Z", name: "API credit (Max 20x)", id: "x", program_id: "subscription_api_credit" } ] });
  if (url === "/api/organizations/org-api/current_spend") return json(200, { amount: 11, resets_at: "2026-11-01T00:00:00Z" });
  if (url === "/api/console/organizations/org-api/workspaces") return json(200, [{ id: "wrkspc_cc", name: "Claude Code" }]);
  if (url.startsWith("/api/organizations/org-api/workspaces/default/usage_cost?")) return json(200, ` + defaultCosts + `);
  if (url.startsWith("/api/organizations/org-api/workspaces/wrkspc_cc/usage_cost?")) return json(200, { costs: { [today]: [] } });
  if (url === "/api/quirky-lollipop/organizations/org-api/link-state") return json(200, { status: "linked", link: { organization: { id: "org-odl", name: "Plan" }, linked_at: "2026-10-08T02:31:07Z" }, plan: "max_20x", monthly_credit_usd_cents: 20000, grants_held_reason: null });
  if (url === "http://127.0.0.1:17343/challenge") return json(200, { nonce: "test-nonce" });
  if (url === "http://127.0.0.1:17343/credits") { posted.push(options.body); return json(200, { message: "Saved API credits for Family's Individual Organization." }); }
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
	var run bookmarkletRun
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("harness output %q: %v", out, err)
	}
	return run
}
