package claudeweb

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExtensionPayloadsAreAcceptedByTheReceiver runs the real extension
// readers in Node against fake claude.ai and Console responses, then posts
// exactly what they produce to the Go receiver.
func TestExtensionPayloadsAreAcceptedByTheReceiver(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	extension, err := filepath.Abs("../../extension")
	if err != nil {
		t.Fatal(err)
	}
	harness := `
const [consolePath, resetsPath] = process.argv.slice(2);
const { readConsole } = require(consolePath);
const { readResets } = require(resetsPath);
const now = Date.now();
const today = new Date(now).toISOString().slice(0, 10);
const nextMonth = new Date(Date.UTC(new Date(now).getUTCFullYear(), new Date(now).getUTCMonth() + 1, 1)).toISOString();
const later = new Date(now + 10 * 86400000).toISOString();
const data = {
  "https://claude.ai/api/organizations": [{ uuid: "org-odl" }],
  "https://claude.ai/api/organizations/org-odl/usage": { cedar_ember: { eligible: true, grants: [
    { resets_left: 1, starts_at: "2026-01-01T00:00:00Z", ends_at: later, paused: false }] } },
  "https://platform.claude.com/api/organizations": [{ uuid: "org-api", name: "Example", capabilities: ["api"] }],
  "https://platform.claude.com/api/organizations/org-api/prepaid/credits": {
    amount: 19989, amount_without_scoped_credits: 100, currency: "usd", balance: { credits: { exponent: 2 } }, tranches: [],
    promo_tranches: [{ name: "API credit", granted_amount_minor_units: 20000, remaining_amount_minor_units: 19888,
      granted_at: "2026-01-01T00:00:00Z", expires_at: later }] },
  "https://platform.claude.com/api/organizations/org-api/current_spend": { amount: 11, resets_at: nextMonth },
  "https://platform.claude.com/api/console/organizations/org-api/workspaces": [],
  "https://platform.claude.com/api/organizations/org-api/workspaces/default/usage_cost": { costs: { [today]: [{ total: 11.2 }] } },
  "https://platform.claude.com/api/quirky-lollipop/organizations/org-api/link-state": {
    status: "linked", link: { organization: { id: "org-odl" } }, plan: "max_20x", monthly_credit_usd_cents: 20000 },
};
const getJSON = async url => {
  const parsed = new URL(url, "https://platform.claude.com");
  const body = data[parsed.origin + parsed.pathname];
  if (body === undefined) throw new Error("unexpected request " + url);
  return { status: 200, body: structuredClone(body) };
};
(async () => {
  const posted = [];
  await readResets(getJSON, Date.now, async body => posted.push({ path: "/v1/resets", body }));
  await readConsole(getJSON, Date.now, {}, async body => posted.push({ path: "/v1/api-credits", body }));
  console.log(JSON.stringify(posted));
})().catch(error => { console.error(error); process.exit(1); });
`
	script := filepath.Join(t.TempDir(), "run.js")
	if err := os.WriteFile(script, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script, filepath.Join(extension, "console.js"), filepath.Join(extension, "resets.js")).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var posted []struct {
		Path string
		Body json.RawMessage
	}
	if err := json.Unmarshal(out, &posted); err != nil || len(posted) != 2 {
		t.Fatalf("posted = %s, %v", out, err)
	}

	isolateUserCache(t)
	var results []Result
	handler := ExtensionHandler(func() []Account { return testAccounts }, func(result Result) { results = append(results, result) })
	for _, post := range posted {
		if got := extensionRequest(handler, "POST", post.Path, extensionAddress, ExtensionOrigins[0], string(post.Body)); got.Code != 204 {
			t.Fatalf("%s = %d %q for %s", post.Path, got.Code, got.Body.String(), post.Body)
		}
	}
	summary, err := ReadSummaryFor("claude:odl")
	if err != nil || len(summary.Credits) != 1 {
		t.Fatalf("resets summary = %+v, %v", summary, err)
	}
	pools, err := ReadAPICreditPools()
	if err != nil || len(pools) != 1 || pools[0].Balance != 19988 || pools[0].LinkedTarget != "claude:odl" {
		t.Fatalf("pools = %+v, %v", pools, err)
	}
	if len(results) != 2 || !results[0].IsResets() || results[1].IsResets() {
		t.Fatalf("results = %+v", results)
	}
}
