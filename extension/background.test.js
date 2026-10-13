"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const vm = require("node:vm");

const tick = () => new Promise(resolve => setImmediate(resolve));
function worker(readConsole, fetch = async () => ({ ok: true, status: 204 }), readResets = async () => ({ status: "ok", payloads: [] }),
  { tabs = [], executeScript = async () => [] } = {}) {
  const listeners = {}, created = [], stored = [], queries = [], injections = [];
  const timers = new Map();
  let timerID = 0;
  const context = vm.createContext({
    importScripts: (...names) => assert.deepEqual(names, ["console.js", "resets.js"]),
    readConsole, readResets, fetch, AbortController, Date,
    setTimeout: (callback, delay) => { assert.equal(delay, 20000); timers.set(++timerID, callback); return timerID; },
    clearTimeout: id => timers.delete(id),
    chrome: {
      alarms: { get: async () => undefined, create: async (...args) => created.push(args),
        onAlarm: { addListener: fn => { listeners.alarm = fn; } } },
      runtime: {
        onInstalled: { addListener: fn => { listeners.install = fn; } },
        onStartup: { addListener: fn => { listeners.startup = fn; } },
      },
      tabs: { query: async filter => { queries.push(filter); return tabs; } },
      scripting: { executeScript: async injection => { injections.push(injection); return executeScript(injection); } },
      storage: { local: { get: async () => ({ cache: {} }), set: async value => stored.push(JSON.parse(JSON.stringify(value))) } },
    },
  });
  vm.runInContext(readFileSync(require.resolve("./background.js"), "utf8"), context);
  return { context, listeners, created, stored, timers, queries, injections };
}

test("alarm lifecycle and one poll at a time", async () => {
  let count = 0, release;
  const w = worker(async () => {
    count++;
    await new Promise(resolve => { release = resolve; });
    return { status: "ok", payloads: [] };
  });
  await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(w.created)), [["poll", { periodInMinutes: 1 }]]);
  // Install and startup each poll at once; the second is skipped while the first runs.
  w.listeners.install(); w.listeners.startup();
  await tick();
  assert.equal(w.created.length, 3);
  assert.equal(count, 1);
  release(); await tick();
  w.listeners.alarm({ name: "other" });
  assert.equal(count, 1);
  w.listeners.alarm({ name: "poll" }); w.listeners.alarm({ name: "poll" });
  await tick();
  assert.equal(count, 2);
  release(); await tick();
  w.listeners.alarm({ name: "poll" }); await tick();
  assert.equal(count, 3);
  release(); await tick();
  assert.equal(w.stored.length, 3);
});

test("authenticated GET and local JSON POST with failures quiet", async () => {
  const requests = [];
  const w = worker(async (getJSON, now, cache, post) => {
    assert.equal(typeof now, "function");
    assert.deepEqual(JSON.parse(JSON.stringify(await getJSON("https://platform.claude.com/api/organizations"))),
      { status: 200, body: [] });
    cache.hashed = { at: now(), daily: {} };
    await post({ pool: "hashed", balance: 10 });
    return { status: "ok", payloads: [{ pool: "hashed", balance: 10 }] };
  }, async (url, options) => {
    requests.push({ url, options });
    if (url.startsWith("http://127.0.0.1")) throw new Error("Not running");
    return { ok: true, status: 200, json: async () => [] };
  });
  await vm.runInContext("poll()", w.context);
  assert.equal(requests.length, 2);
  assert.equal(requests[0].options.credentials, "include");
  assert.equal(requests[0].options.headers.Accept, "application/json");
  assert.equal(requests[0].options.redirect, "error");
  assert.equal(requests[1].url, "http://127.0.0.1:17344/v1/api-credits");
  assert.equal(requests[1].options.method, "POST");
  assert.equal(requests[1].options.credentials, "omit");
  assert.equal(w.stored.length, 1);
  assert.equal(w.timers.size, 0);
});

test("signed out posts status and removes saved cost cache", async () => {
  const posts = [];
  const w = worker(async () => ({ status: "signed_out", payloads: [] }),
    async (url, options) => { posts.push([url, JSON.parse(options.body)]); return {}; });
  await vm.runInContext("poll()", w.context);
  assert.deepEqual(posts, [["http://127.0.0.1:17344/v1/status", { status: "signed_out" }]]);
  assert.deepEqual(w.stored, [{ cache: {} }]);
});

test("timeout covers response JSON and a failed poll can run again", async () => {
  let calls = 0;
  const w = worker(async getJSON => {
    calls++;
    await getJSON("https://platform.claude.com/api/organizations");
    return { status: "ok", payloads: [] };
  }, async (_url, options) => ({
    ok: true, status: 200,
    json: () => new Promise((_resolve, reject) => options.signal.addEventListener("abort", () => reject(new Error("Timeout")))),
  }));
  const pending = vm.runInContext("poll()", w.context);
  await tick();
  assert.equal(w.timers.size, 1);
  [...w.timers.values()][0]();
  await pending;
  assert.equal(w.timers.size, 0);
  const again = vm.runInContext("poll()", w.context);
  await tick();
  [...w.timers.values()][0]();
  await again;
  assert.equal(calls, 2);
});

test("an org is posted even if a later org fails", async () => {
  const posts = [];
  const w = worker(async (_get, _now, _cache, post) => {
    await post({ pool: "first", observed_at: "2026-10-08T12:00:00Z" });
    throw new Error("second org failed");
  }, async (_url, options) => { posts.push(JSON.parse(options.body)); return {}; });
  await vm.runInContext("poll()", w.context);
  assert.equal(posts.length, 1);
  assert.equal(posts[0].pool, "first");
});

test("resets post to their own route and a resets failure never blocks credits", async () => {
  const posts = [];
  let credits = 0;
  const w = worker(async () => { credits++; return { status: "ok", payloads: [] }; },
    async (url, options) => { posts.push([url, JSON.parse(options.body)]); return {}; },
    async (getJSON, now, post) => {
      assert.equal(typeof getJSON, "function");
      assert.equal(typeof now, "function");
      await post({ organization: "org-plan", grants: [] });
      throw new Error("claude.ai changed format");
    });
  await vm.runInContext("poll()", w.context);
  assert.deepEqual(posts, [["http://127.0.0.1:17344/v1/resets", { organization: "org-plan", grants: [] }]]);
  assert.equal(credits, 1);
});

test("claude.ai is read inside an open claude.ai tab, falling back without one", async () => {
  const url = "https://claude.ai/api/organizations";
  const fetched = [];
  const fetch = async (requested, options) => {
    fetched.push([requested, options]);
    return { ok: false, status: 403, json: async () => null };
  };
  const readThrough = (tabs, executeScript) => {
    let answer;
    const w = worker(async () => ({ status: "ok", payloads: [] }), fetch,
      async getJSON => { answer = await getJSON(url); return { status: "ok", payloads: [] }; }, { tabs, executeScript });
    return vm.runInContext("poll()", w.context).then(() => ({ w, answer: JSON.parse(JSON.stringify(answer)) }));
  };

  // The first tab that answers wins; a tab that cannot run the script is skipped.
  let { w, answer } = await readThrough([{ id: 3 }, { id: 7 }], async ({ target }) => {
    if (target.tabId === 3) throw new Error("Cannot access contents of the page");
    return [{ result: { status: 200, body: [{ uuid: "org" }] } }];
  });
  assert.deepEqual(answer, { status: 200, body: [{ uuid: "org" }] });
  assert.deepEqual(JSON.parse(JSON.stringify(w.queries)), [{ url: "https://claude.ai/*", discarded: false, status: "complete" }]);
  assert.deepEqual(w.injections.map(i => i.target.tabId), [3, 7]);
  assert.equal(w.injections[1].func.name, "readInPage");
  assert.deepEqual(JSON.parse(JSON.stringify(w.injections[1].args)), [url]);
  assert.equal(w.injections[1].world, undefined, "the script runs in the isolated world, not the page's");
  assert.equal(fetched.filter(([requested]) => requested === url).length, 0);

  // No tab, or a tab whose script returns nothing usable, falls back to the worker.
  for (const [tabs, executeScript] of [[[], undefined], [[{ id: 3 }], async () => [{ result: undefined }]]]) {
    fetched.length = 0;
    ({ answer } = await readThrough(tabs, executeScript));
    assert.deepEqual(answer, { status: 403, body: null });
    assert.equal(fetched[0][0], url);
    assert.equal(fetched[0][1].credentials, "include");
  }
});

test("the in-tab read is self-contained and asks for JSON with the page's session", async () => {
  const w = worker(async () => ({ status: "ok", payloads: [] }));
  const source = vm.runInContext("readInPage.toString()", w.context);
  const calls = [];
  // executeScript serializes the function, so it must run with no worker globals.
  const page = vm.createContext({
    AbortSignal: { timeout: ms => ({ timeout: ms }) },
    fetch: async (url, options) => { calls.push([url, options]); return { ok: true, status: 200, json: async () => ({ grants: [] }) }; },
  });
  const result = await vm.runInContext(`(${source})("https://claude.ai/api/organizations")`, page);
  assert.deepEqual(JSON.parse(JSON.stringify(result)), { status: 200, body: { grants: [] } });
  assert.deepEqual(JSON.parse(JSON.stringify(calls)), [["https://claude.ai/api/organizations",
    { credentials: "include", cache: "no-store", redirect: "error", headers: { Accept: "application/json" }, signal: { timeout: 20000 } }]]);
});
