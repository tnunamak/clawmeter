"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readResets } = require("./resets.js");

const now = Date.parse("2026-10-13T12:00:00Z");
const grant = { resets_left: 2, starts_at: "2026-10-01T00:00:00Z", ends_at: "2026-11-01T00:00:00Z", paused: false };
function fixture(data) {
  const calls = [];
  const getJSON = async url => {
    const parsed = new URL(url);
    assert.equal(parsed.origin, "https://claude.ai");
    const path = parsed.pathname + parsed.search;
    calls.push(path);
    const body = data[path];
    if (typeof body === "number") return { status: body, body: null };
    if (body === undefined) return { status: 404, body: null };
    return { status: 200, body };
  };
  return { calls, getJSON };
}

test("posts each organization's grants with its ID and observation time", async () => {
  const { calls, getJSON } = fixture({
    "/api/organizations": [{ uuid: "org-max" }, { uuid: "org-api" }, { uuid: "org-max" }],
    "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": { cedar_ember: { eligible: true, grants: [grant, { resets_left: 0 }] } },
    "/api/organizations/org-api/usage?at_wall=1&skip_spend=1": 403,
  });
  const posts = [];
  const result = await readResets(getJSON, () => now, async payload => posts.push(payload));
  assert.equal(result.status, "ok");
  assert.deepEqual(calls, ["/api/organizations", "/api/organizations/org-max/usage?at_wall=1&skip_spend=1",
    "/api/organizations/org-api/usage?at_wall=1&skip_spend=1"]);
  assert.deepEqual(posts, [{
    observed_at: "2026-10-13T12:00:00.000Z", organization: "org-max",
    grants: [grant, { resets_left: 0, starts_at: undefined, ends_at: undefined, paused: undefined }],
  }]);
});

test("an ineligible organization sends no grants", async () => {
  const { getJSON } = fixture({
    "/api/organizations": { organizations: [{ id: "org-max" }] },
    "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": { cedar_ember: { eligible: false, grants: [grant] } },
  });
  const posts = [];
  await readResets(getJSON, now, async payload => posts.push(payload));
  assert.deepEqual(posts.map(payload => payload.grants), [[]]);
});

test("an organization without reset data is skipped", async () => {
  const { getJSON } = fixture({
    "/api/organizations": [{ uuid: "org-max" }],
    "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": { five_hour: {} },
  });
  const posts = [];
  await readResets(getJSON, now, async payload => posts.push(payload));
  assert.deepEqual(posts, []);
});

test("signed out sends nothing", async () => {
  const { getJSON } = fixture({ "/api/organizations": 401 });
  const posts = [];
  assert.deepEqual(await readResets(getJSON, now, async payload => posts.push(payload)), { status: "signed_out", payloads: [] });
  assert.deepEqual(posts, []);
});

test("changed grant data sends nothing for that organization", async () => {
  for (const bad of [{ ...grant, paused: "no" }, { ...grant, ends_at: "soon" }, { ...grant, resets_left: -1 }, { ...grant, resets_left: 1.5 }]) {
    const { getJSON } = fixture({
      "/api/organizations": [{ uuid: "org-max" }],
      "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": { cedar_ember: { grants: [bad] } },
    });
    const posts = [];
    await assert.rejects(readResets(getJSON, now, async payload => posts.push(payload)), /changed format/);
    assert.deepEqual(posts, []);
  }
});

test("a server error stops the poll", async () => {
  const { getJSON } = fixture({ "/api/organizations": [{ uuid: "org-max" }], "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": 500 });
  await assert.rejects(readResets(getJSON, now), /request failed/);
});

test("each organization is dated by its own read", async () => {
  const { getJSON } = fixture({
    "/api/organizations": [{ uuid: "org-a" }, { uuid: "org-b" }],
    "/api/organizations/org-a/usage?at_wall=1&skip_spend=1": { cedar_ember: { grants: [] } },
    "/api/organizations/org-b/usage?at_wall=1&skip_spend=1": { cedar_ember: { grants: [] } },
  });
  let clock = now;
  const slow = async url => { clock += 60000; return getJSON(url); };
  const posts = [];
  await readResets(slow, () => clock, async payload => posts.push(payload));
  assert.deepEqual(posts.map(payload => payload.observed_at), ["2026-10-13T12:02:00.000Z", "2026-10-13T12:03:00.000Z"]);
});

test("names each organization and what Clawmeter did with its reading", async () => {
  const { getJSON } = fixture({
    "/api/organizations": [{ uuid: "org-max", name: "Tim's Max" }, { uuid: "org-other", name: "Other" }],
    "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": { cedar_ember: { eligible: true, grants: [grant] } },
    "/api/organizations/org-other/usage?at_wall=1&skip_spend=1": { cedar_ember: { eligible: true, grants: [] } },
  });
  const result = await readResets(getJSON, now, async payload => payload.organization === "org-max" ? "saved" : "ignored");
  assert.deepEqual(result.accounts, [
    { name: "Tim's Max", resets: 2, delivery: "saved" },
    { name: "Other", resets: 0, delivery: "ignored" },
  ]);
});

test("the popup count holds only resets usable now, and one failing organization hides no other", async () => {
  const paused = { ...grant, paused: true };
  const expired = { ...grant, ends_at: "2026-10-12T00:00:00Z" };
  const future = { ...grant, starts_at: "2026-10-20T00:00:00Z" };
  const { getJSON } = fixture({
    "/api/organizations": [{ uuid: "org-bad", name: "Bad" }, { uuid: "org-max", name: "Max" }],
    "/api/organizations/org-bad/usage?at_wall=1&skip_spend=1": 500,
    "/api/organizations/org-max/usage?at_wall=1&skip_spend=1": { cedar_ember: { eligible: true, grants: [grant, paused, expired, future] } },
  });
  const posts = [];
  const result = await readResets(getJSON, now, async payload => { posts.push(payload); return "saved"; });
  assert.deepEqual(posts.map(payload => payload.grants.length), [4]);
  assert.deepEqual(result.accounts, [
    { name: "Bad", error: "Claude request failed" },
    { name: "Max", resets: 2, delivery: "saved" },
  ]);
});
