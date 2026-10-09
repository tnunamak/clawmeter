"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { createHash, webcrypto } = require("node:crypto");
const vm = require("node:vm");
const { readConsole } = require("./console.js");

const now = Date.parse("2026-10-08T12:00:00Z");
const today = "2026-10-08";
const poolHash = id => createHash("sha256").update("clawmeter-api-pool\u0000" + id).digest("hex");
function fixture() {
  const calls = [];
  const data = {
    "/api/organizations": [{ uuid: "org-api", name: "Example", capabilities: ["api"] }],
    "/api/organizations/org-api/prepaid/credits": {
      amount: 19989, amount_without_scoped_credits: 100, currency: "usd",
      balance: { credits: { exponent: 2 } }, tranches: [],
      promo_tranches: [{ name: "API credit", granted_amount_minor_units: 20000,
        remaining_amount_minor_units: 19888, granted_at: "2026-10-01T00:00:00Z",
        expires_at: "2026-10-25T00:00:00Z" }],
    },
    "/api/organizations/org-api/current_spend": { amount: 11, resets_at: "2026-11-01T00:00:00Z" },
    "/api/console/organizations/org-api/workspaces": [{ id: "other" }],
    "/api/organizations/org-api/workspaces/default/usage_cost": {
      costs: { [today]: [{ total: 5 }] }, web_search_costs: { [today]: [{ total: 2 }] },
      code_execution_costs: { [today]: [{ total: 1 }] }, session_usage_costs: { [today]: [{ total: 1 }] },
      claude_code_savings: { [today]: [{ total: 9999 }] },
    },
    "/api/organizations/org-api/workspaces/other/usage_cost": { costs: { [today]: [{ total: 2.29645 }] } },
    "/api/quirky-lollipop/organizations/org-api/link-state": {
      status: "linked", link: { organization: { id: "org-plan" } }, plan: "max_20x", monthly_credit_usd_cents: 20000,
    },
  };
  const statuses = {};
  const getJSON = async url => {
    const parsed = new URL(url, "https://platform.claude.com");
    assert.equal(parsed.origin, "https://platform.claude.com");
    calls.push(parsed.pathname + parsed.search);
    if (!(parsed.pathname in data) && !(parsed.pathname in statuses)) throw new Error("Unexpected fixture request");
    return { status: statuses[parsed.pathname] || 200, body: structuredClone(data[parsed.pathname]) };
  };
  return { data, calls, statuses, getJSON };
}

test("healthy balance, all cost kinds, grants and plan link", async () => {
  const f = fixture(), cache = {};
  const { payloads } = await readConsole(f.getJSON, now, cache);
  assert.deepEqual(payloads, [{
    pool: poolHash("org-api"), name: "Example", currency: "USD", balance: 19988,
    grants: [{ name: "API credit", granted: 20000, remaining: 19888,
      granted_at: "2026-10-01T00:00:00Z", expires_at: "2026-10-25T00:00:00Z" }],
    month_spend: 11, month_resets_at: "2026-11-01T00:00:00Z",
    daily: { [today]: 11.29645 },
    link: { organization: "org-plan", plan: "max_20x", monthly_credit: 20000 },
  }]);
  assert.equal(JSON.stringify(cache).includes("org-"), false);
  assert.ok(f.calls.filter(url => url.includes("usage_cost")).every(url =>
    url.includes("starting_on=2026-10-01&ending_before=2026-10-09")));
});

test("billing-forbidden org is skipped and another org is returned", async () => {
  for (const endpoint of ["prepaid/credits", "current_spend"]) {
    const f = fixture();
    f.data["/api/organizations"].unshift({ uuid: "forbidden", capabilities: ["api"] });
    f.statuses["/api/organizations/forbidden/" + endpoint] = 403;
    f.statuses["/api/organizations/forbidden/" + (endpoint === "current_spend" ? "prepaid/credits" : "current_spend")] = 200;
    const result = await readConsole(f.getJSON, now, {});
    assert.equal(result.payloads.length, 1);
    assert.equal(result.payloads[0].pool, poolHash("org-api"));
  }
});

test("organization authorization failures signal signed out", async () => {
  for (const status of [401, 403]) {
    const f = fixture();
    f.statuses["/api/organizations"] = status;
    assert.deepEqual(await readConsole(f.getJSON, now, {}), { status: "signed_out", payloads: [] });
  }
});

test("costs that never matched spend skip the pool; shape changes are rejected", async () => {
  const f = fixture();
  f.data["/api/organizations/org-api/current_spend"].amount = 100;
  assert.deepEqual(await readConsole(f.getJSON, now, {}), { status: "ok", payloads: [] });
  for (const mutate of [
    f => { f.data["/api/organizations"] = {}; },
    f => { f.data["/api/organizations/org-api/workspaces/default/usage_cost"] = {}; },
    f => { f.data["/api/organizations/org-api/prepaid/credits"].promo_tranches = {}; },
    f => { f.data["/api/organizations/org-api/workspaces/default/usage_cost"].costs[today][0].total = "5"; },
  ]) {
    const f = fixture();
    mutate(f);
    await assert.rejects(readConsole(f.getJSON, now, {}), /changed format/);
  }
});

test("cache throttles costs but credits, spend and link are polled again", async () => {
  const f = fixture(), cache = {};
  await readConsole(f.getJSON, now, cache);
  const costCount = () => f.calls.filter(url => url.includes("usage_cost")).length;
  await readConsole(f.getJSON, now + 29 * 60000, cache);
  assert.equal(costCount(), 2);
  assert.equal(f.calls.filter(url => url.endsWith("link-state")).length, 2);
  assert.equal(f.calls.filter(url => url.endsWith("prepaid/credits")).length, 2);
  await readConsole(f.getJSON, now + 30 * 60000, cache);
  assert.equal(costCount(), 4);
  // Spend moves ahead of the cost report: the balance and spend stay
  // current, the last matching costs are kept, and costs are refetched once
  // per usageRetry until they match again.
  f.data["/api/organizations/org-api/current_spend"].amount = 1000;
  f.data["/api/organizations/org-api/prepaid/credits"].promo_tranches[0].remaining_amount_minor_units = 18900;
  f.data["/api/organizations/org-api/prepaid/credits"].amount = 19000;
  let { payloads } = await readConsole(f.getJSON, now + 31 * 60000, cache);
  assert.equal(payloads[0].month_spend, 1000);
  assert.equal(payloads[0].balance, 19000);
  assert.deepEqual(payloads[0].daily, { [today]: 11.29645 });
  assert.equal(costCount(), 4);
  await readConsole(f.getJSON, now + 34 * 60000, cache);
  assert.equal(costCount(), 4);
  await readConsole(f.getJSON, now + 35 * 60000, cache);
  assert.equal(costCount(), 6);
  f.data["/api/organizations/org-api/workspaces/other/usage_cost"].costs[today][0].total = 991;
  ({ payloads } = await readConsole(f.getJSON, now + 40 * 60000, cache));
  assert.equal(costCount(), 8);
  assert.deepEqual(payloads[0].daily, { [today]: 1000 });
});

test("month start includes the last seven days and month end includes every month day", async () => {
  for (const [date, start] of [["2026-10-01T12:00:00Z", "2026-09-25"], ["2026-10-31T12:00:00Z", "2026-10-01"]]) {
    const f = fixture();
    await readConsole(f.getJSON, Date.parse(date), {});
    assert.ok(f.calls.filter(url => url.includes("usage_cost")).every(url => url.includes("starting_on=" + start)));
  }
});

test("payload matches the real bookmarklet except nonce and link proof", async () => {
  const f = fixture(), posted = [], alerts = [];
  class Clock extends Date {
    constructor(...args) { super(...(args.length ? args : [now])); }
    static now() { return now; }
  }
  const context = {
    Date: Clock, crypto: webcrypto, TextEncoder,
    location: { origin: "https://platform.claude.com" },
    document: { cookie: "lastActiveOrg=org-api" }, window: {},
    alert: message => alerts.push(message),
    fetch: async (url, options = {}) => {
      if (url.endsWith("/challenge")) return { ok: true, json: async () => ({ nonce: "test-nonce" }) };
      if (url === "http://127.0.0.1:17343/credits") {
        posted.push(JSON.parse(options.body));
        return { ok: true, json: async () => ({ message: "Saved" }) };
      }
      const response = await f.getJSON(url);
      return { ok: response.status === 200, status: response.status, json: async () => response.body };
    },
  };
  await vm.runInNewContext(readFileSync(require.resolve("../internal/claudeweb/bookmarklet.js"), "utf8"), context);
  assert.deepEqual(alerts, ["Clawmeter: Saved"]);
  assert.equal(posted.length, 1);
  const extension = (await readConsole(f.getJSON, now, {})).payloads[0];
  const bookmark = posted[0];
  const proof = createHash("sha256").update("clawmeter-claude-reset\u0000test-nonce\u0000org-plan").digest("hex");
  assert.equal(bookmark.link.account, proof);
  delete bookmark.nonce;
  bookmark.link = { organization: "org-plan", plan: bookmark.link.plan, monthly_credit: bookmark.link.monthly_credit };
  assert.deepEqual(extension, bookmark);
});

test("manifest key fixes the expected unpacked identity and minimal permissions", () => {
  const manifest = JSON.parse(readFileSync(require.resolve("./manifest.json"), "utf8"));
  const digest = createHash("sha256").update(Buffer.from(manifest.key, "base64")).digest("hex").slice(0, 32);
  assert.equal([...digest].map(c => String.fromCharCode(97 + parseInt(c, 16))).join(""), "hfpaojbfhfofabcaappnobhbfddeckjm");
  assert.deepEqual(manifest.permissions, ["alarms", "storage"]);
  assert.deepEqual(manifest.host_permissions, ["https://platform.claude.com/*", "http://127.0.0.1/*"]);
});

test("multiple billing orgs have separate cached reports", async () => {
  const f = fixture(), cache = {};
  f.data["/api/organizations"].push({ uuid: "second", name: "Second", capabilities: ["api"] });
  for (const [path, body] of Object.entries(f.data)) {
    if (path.includes("org-api")) f.data[path.replace("org-api", "second")] = structuredClone(body);
  }
  const first = await readConsole(f.getJSON, now, cache);
  assert.equal(first.payloads.length, 2);
  assert.deepEqual(Object.keys(cache).sort(), [poolHash("org-api"), poolHash("second")].sort());
  await readConsole(f.getJSON, now + 60000, cache);
  assert.equal(f.calls.filter(url => url.includes("usage_cost")).length, 4);
});
