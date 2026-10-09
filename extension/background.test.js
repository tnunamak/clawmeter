"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const vm = require("node:vm");

const tick = () => new Promise(resolve => setImmediate(resolve));
function worker(readConsole, fetch = async () => ({ ok: true, status: 204 })) {
  const listeners = {}, created = [], stored = [];
  const timers = new Map();
  let timerID = 0;
  const context = vm.createContext({
    importScripts: name => assert.equal(name, "console.js"),
    readConsole, fetch, AbortController, Date,
    setTimeout: (callback, delay) => { assert.equal(delay, 20000); timers.set(++timerID, callback); return timerID; },
    clearTimeout: id => timers.delete(id),
    chrome: {
      alarms: { get: async () => undefined, create: async (...args) => created.push(args),
        onAlarm: { addListener: fn => { listeners.alarm = fn; } } },
      runtime: {
        onInstalled: { addListener: fn => { listeners.install = fn; } },
        onStartup: { addListener: fn => { listeners.startup = fn; } },
      },
      storage: { local: { get: async () => ({ cache: {} }), set: async value => stored.push(JSON.parse(JSON.stringify(value))) } },
    },
  });
  vm.runInContext(readFileSync(require.resolve("./background.js"), "utf8"), context);
  return { context, listeners, created, stored, timers };
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
