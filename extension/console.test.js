"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { createHash } = require("node:crypto");
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
    pool: poolHash("org-api"), observed_at: new Date(now).toISOString(), name: "Example", currency: "USD", balance: 19988,
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

// The Console state of 2026-10-13: credits used up and $4.50 spent past them.
test("unpaid negative balance is sent, not rejected as a format change", async () => {
  const f = fixture(), cache = {};
  f.data["/api/organizations/org-api/prepaid/credits"] = { amount: -450, amount_without_scoped_credits: -450, currency: "USD",
    balance: { money: null, credits: { amount_minor: 0, exponent: 2 } }, balance_credits: 0, tranches: [], promo_tranches: [] };
  f.data["/api/organizations/org-api/current_spend"] = { amount: 20450, resets_at: "2026-11-01T00:00:00Z" };
  f.data["/api/organizations/org-api/workspaces/default/usage_cost"] = { costs: { [today]: [{ total: 20450 }] } };
  f.data["/api/organizations/org-api/workspaces/other/usage_cost"] = { costs: {} };
  const { payloads } = await readConsole(f.getJSON, now, cache);
  assert.equal(payloads.length, 1);
  assert.equal(payloads[0].balance, -450);
  assert.deepEqual(payloads[0].grants, []);
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
  const skipped = await readConsole(f.getJSON, now, {});
  assert.deepEqual({ ...skipped, listed: undefined }, { status: "ok", payloads: [], failures: [], listed: undefined });
  // The pool is still listed: this session holds it even though it was skipped.
  assert.equal(skipped.listed.length, 1);
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

test("manifest key fixes the expected unpacked identity and minimal permissions", () => {
  const manifest = JSON.parse(readFileSync(require.resolve("./manifest.json"), "utf8"));
  const digest = createHash("sha256").update(Buffer.from(manifest.key, "base64")).digest("hex").slice(0, 32);
  assert.equal([...digest].map(c => String.fromCharCode(97 + parseInt(c, 16))).join(""), "hfpaojbfhfofabcaappnobhbfddeckjm");
  assert.deepEqual(manifest.permissions, ["alarms", "scripting", "storage"]);
  assert.deepEqual(manifest.host_permissions, ["https://claude.ai/*", "https://platform.claude.com/*", "http://127.0.0.1/*"]);
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

test("retained costs expire without moving reconciliation time", async () => {
  const f = fixture(), cache = {}, pool = poolHash("org-api");
  await readConsole(f.getJSON, now, cache);
  assert.equal(cache[pool].reconciledAt, now);
  f.data["/api/organizations/org-api/current_spend"].amount = 1000;
  assert.equal((await readConsole(f.getJSON, now + 59 * 60000, cache)).payloads.length, 1);
  assert.equal(cache[pool].at, now + 59 * 60000);
  assert.equal(cache[pool].reconciledAt, now);
  // Also enforce the bound on a throttled poll, without another report.
  assert.equal((await readConsole(f.getJSON, now + 61 * 60000, cache)).payloads.length, 0);
  assert.equal((await readConsole(f.getJSON, now + 65 * 60000, cache)).payloads.length, 0);
  assert.equal(cache[pool].reconciledAt, now);
  delete cache[pool].reconciledAt;
  assert.equal((await readConsole(f.getJSON, now + 66 * 60000, cache)).payloads.length, 0);
});

test("each org is timestamped at its requests and posted before the next org", async () => {
  const f = fixture(), posts = [];
  f.data["/api/organizations"].push({ uuid: "second", name: "Second", capabilities: ["api"] });
  for (const [path, body] of Object.entries(f.data)) {
    if (path.includes("org-api")) f.data[path.replace("org-api", "second")] = structuredClone(body);
  }
  let clock = now;
  const get = async url => {
    if (url.includes("/second/prepaid/credits")) {
      assert.equal(posts.length, 1);
      assert.equal(posts[0].observed_at, new Date(now).toISOString());
      clock += 60000; // The response latency must not become observation time.
    }
    return f.getJSON(url);
  };
  const result = await readConsole(get, () => clock, {}, async payload => {
    posts.push(payload);
    clock += 120000;
  });
  assert.equal(posts.length, 2);
  assert.equal(result.payloads[1].observed_at, new Date(now + 120000).toISOString());
});

test("a failing organization is reported and does not hide the others", async () => {
  const f = fixture();
  f.data["/api/organizations"].unshift({ uuid: "org-broken", name: "Broken", capabilities: ["api"] });
  f.statuses["/api/organizations/org-broken/prepaid/credits"] = 500;
  f.data["/api/organizations/org-broken/current_spend"] = { amount: 0, resets_at: "2026-11-01T00:00:00Z" };
  const posted = [];
  const result = await readConsole(f.getJSON, now, {}, async payload => posted.push(payload));
  assert.deepEqual(posted.map(payload => payload.name), ["Example"]);
  assert.deepEqual(result.failures, [{ name: "Broken", error: "Claude Console request failed" }]);
});

test("a cost report that fails to load keeps the fresh balance on retained costs", async () => {
  const f = fixture(), cache = {};
  await readConsole(f.getJSON, now, cache);
  f.statuses["/api/organizations/org-api/workspaces/default/usage_cost"] = 500;
  f.data["/api/organizations/org-api/prepaid/credits"].amount = 19000;
  f.data["/api/organizations/org-api/prepaid/credits"].promo_tranches[0].remaining_amount_minor_units = 18900;
  const { payloads } = await readConsole(f.getJSON, now + 30 * 60000, cache);
  assert.equal(payloads[0].balance, 19000);
  assert.deepEqual(payloads[0].daily, { [today]: 11.29645 });
  // Past the retained age, there are no costs to show.
  await assert.rejects(readConsole(f.getJSON, now + 61 * 60000, {}), /request failed/);
  assert.deepEqual((await readConsole(f.getJSON, now + 61 * 60000, cache)).payloads, []);
});

test("a caller reading several sessions shares the pools seen and keeps other sessions' cache", async () => {
  const f = fixture();
  const cache = { [poolHash("other-session")]: { at: now, reconciledAt: now, daily: {} } };
  const pools = { seen: new Set(), read: new Set() };
  await readConsole(f.getJSON, now, cache, async () => {}, pools);
  assert.deepEqual([...pools.read], [poolHash("org-api")]);
  assert.ok(cache[poolHash("other-session")]);
  // A pool already read in another session is skipped.
  const again = await readConsole(f.getJSON, now, cache, async () => {}, pools);
  assert.deepEqual(again.payloads, []);
});

test("a pool one session cannot read is read by the next", async () => {
  const forbidden = fixture(), healthy = fixture();
  forbidden.statuses["/api/organizations/org-api/prepaid/credits"] = 403;
  const pools = { seen: new Set(), read: new Set() }, cache = {}, posted = [];
  await readConsole(forbidden.getJSON, now, cache, async payload => posted.push(payload), pools);
  await readConsole(healthy.getJSON, now, cache, async payload => posted.push(payload), pools);
  assert.equal(posted.length, 1);
  assert.deepEqual([...pools.seen], [poolHash("org-api")]);
});
