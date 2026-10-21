"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const vm = require("node:vm");

const tick = () => new Promise(resolve => setImmediate(resolve));
function worker(readConsole, fetch = async () => ({ ok: true, status: 204 }), readResets = async () => ({ status: "ok", payloads: [], accounts: [] }),
  { tabs = [], executeScript = async () => [] } = {}) {
  const listeners = {}, created = [], stored = [], sessions = [], queries = [], injections = [], badges = [], opened = [];
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
        id: "self",
        onMessage: { addListener: fn => { listeners.message = fn; } },
        onInstalled: { addListener: fn => { listeners.install = fn; } },
        onStartup: { addListener: fn => { listeners.startup = fn; } },
      },
      // A tab without a url is a claude.ai tab.
      action: { setBadgeBackgroundColor: async () => {}, setBadgeText: async ({ text }) => badges.push(text),
        setTitle: async ({ title }) => badges.push(title) },
      tabs: { create: async tab => { opened.push(tab); return { id: 7 }; },
        onUpdated: { addListener: fn => { listeners.updated = fn; }, removeListener: () => { listeners.updated = null; } },
        query: async filter => {
        queries.push(filter);
        return tabs.filter(tab => (tab.url || "https://claude.ai/").startsWith(filter.url.slice(0, -1)))
          .map(tab => ({ discarded: false, status: "complete", ...tab }));
      } },
      scripting: { executeScript: async injection => { injections.push(injection); return executeScript(injection); } },
      storage: { local: { get: async () => ({ cache: {} }), set: async value => stored.push(JSON.parse(JSON.stringify(value))) },
        session: { set: async value => sessions.push(JSON.parse(JSON.stringify(value))) } },
    },
  });
  vm.runInContext(readFileSync(require.resolve("./background.js"), "utf8"), context);
  return { context, listeners, created, stored, sessions, timers, queries, injections, badges, opened };
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
  assert.deepEqual(w.stored, [{ cache: {}, sessions: {} }]);
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

  // A tab that cannot run the script is skipped.
  let { w, answer } = await readThrough([{ id: 3 }, { id: 7 }], async ({ target }) => {
    if (target.tabId === 3) throw new Error("Cannot access contents of the page");
    return [{ result: { status: 200, body: [{ uuid: "org" }] } }];
  });
  assert.deepEqual(answer, { status: 200, body: [{ uuid: "org" }] });
  assert.deepEqual(JSON.parse(JSON.stringify(w.queries)), [{ url: "https://claude.ai/*" }, { url: "https://platform.claude.com/*" }]);
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

test("a claude.ai tab that never answers is abandoned for the next one", async () => {
  const url = "https://claude.ai/api/organizations";
  let answer;
  const w = worker(async () => ({ status: "ok", payloads: [] }), undefined,
    async getJSON => { answer = await getJSON(url); return { status: "ok", payloads: [] }; },
    { tabs: [{ id: 3 }, { id: 7 }], executeScript: ({ target }) => target.tabId === 3
      ? new Promise(() => {}) : Promise.resolve([{ result: { status: 200, body: [] } }]) });
  const pending = vm.runInContext("poll()", w.context);
  await tick();
  assert.equal(w.injections.length, 1);
  assert.equal(w.timers.size, 1);
  [...w.timers.values()][0]();
  await pending;
  assert.deepEqual(JSON.parse(JSON.stringify(answer)), { status: 200, body: [] });
  assert.deepEqual(w.injections.map(i => i.target.tabId), [3, 7]);
  assert.equal(w.timers.size, 0);
});

test("each poll leaves a status for the popup, and the popup can ask for a poll", async () => {
  const resetsSignedOut = async () => ({ status: "signed_out", payloads: [] });
  const consoleRead = async (getJSON, now, cache, post) => {
    await post({ pool: "hashed" });
    return { status: "ok", payloads: [{ name: "Team", balance: 1250, currency: "USD" }],
      failures: [{ name: "Broken", error: "Claude Console request failed" }] };
  };
  const w = worker(consoleRead, async () => { throw new Error("connection refused"); }, resetsSignedOut);
  let replied;
  assert.equal(w.listeners.message("poll", { id: "self" }, value => { replied = value; }), true);
  await tick(); await tick();
  assert.equal(replied, true);
  const { status } = w.sessions.at(-1);
  // No claude.ai tab: the worker's 403 means "open a tab", not "signed out".
  assert.deepEqual(status.claude, { state: "no_tab" });
  assert.deepEqual(status.console, { state: "ok", unchecked: 0, pools: [{ name: "Team", balance: 1250, currency: "USD" }],
    failures: [{ name: "Broken", error: "Claude Console request failed" }] });
  assert.deepEqual(status.delivery, ["unreachable"]);
  assert.equal(w.listeners.message("poll", { id: "other" }, () => {}), undefined);
});

test("a popup refresh during a running check waits for that check", async () => {
  let release;
  const w = worker(async () => {
    await new Promise(resolve => { release = resolve; });
    return { status: "ok", payloads: [], failures: [] };
  });
  w.listeners.alarm({ name: "poll" });
  await tick();
  let replied = false;
  w.listeners.message("poll", { id: "self" }, () => { replied = true; });
  await tick();
  assert.equal(replied, false);
  release(); await tick(); await tick();
  assert.equal(replied, true);
  assert.equal(w.sessions.length, 1);
});

test("each claude.ai tab is read as its own session, and an organization is read once", async () => {
  const { readResets } = require("./resets.js");
  const grant = { resets_left: 1, starts_at: "2026-01-01T00:00:00Z", ends_at: "2099-01-01T00:00:00Z", paused: false };
  // Tabs 1 and 2 are signed in to different accounts; both see the team.
  const sessions = {
    1: [{ uuid: "org-a", name: "A" }, { uuid: "org-team", name: "Team" }],
    2: [{ uuid: "org-b", name: "B" }, { uuid: "org-team", name: "Team" }],
  };
  const reads = [];
  const executeScript = async ({ target, args: [url] }) => {
    const path = new URL(url).pathname;
    reads.push(target.tabId + " " + path);
    if (path === "/api/organizations") return [{ result: { status: 200, body: sessions[target.tabId] } }];
    return [{ result: { status: 200, body: { cedar_ember: { eligible: true, grants: [grant] } } } }];
  };
  const w = worker(async () => ({ status: "ok", payloads: [], failures: [] }), undefined, readResets,
    { tabs: [{ id: 1 }, { id: 2 }], executeScript });
  await vm.runInContext("poll()", w.context);
  assert.deepEqual(w.sessions.at(-1).status.claude.accounts.map(account => account.name), ["A", "Team", "B"]);
  assert.equal(reads.filter(read => read.includes("org-team")).length, 1);
});

test("a Console tab that does not answer keeps its cache and blocks a global sign-out", async () => {
  const cached = { at: Date.now(), reconciledAt: Date.now(), daily: {} };
  const consoleRead = async () => ({ status: "signed_out", payloads: [] });
  const w = worker(consoleRead, undefined, undefined, {
    tabs: [{ id: 1, url: "https://platform.claude.com/" }, { id: 2, url: "https://platform.claude.com/" }],
    executeScript: async () => { throw new Error("frozen"); },
  });
  w.context.chrome.storage.local.get = async () => ({ cache: { kept: cached } });
  // Tab 1 never answers; the reader in tab 2 reports a sign-out.
  w.context.readConsole = async (read, now, cache) => {
    await read("https://platform.claude.com/api/organizations");
    return { status: "signed_out", payloads: [] };
  };
  await vm.runInContext("poll()", w.context);
  assert.deepEqual(w.stored.at(-1).cache, { kept: cached });
  assert.equal(w.sessions.at(-1).status.console.state, "error");
});

test("a discarded or loading Console tab, or a failed tab query, keeps saved costs and blocks a global sign-out", async () => {
  const cached = { at: Date.now(), reconciledAt: Date.now(), daily: {} };
  for (const [tabs, failQuery] of [
    [[{ id: 1, url: "https://platform.claude.com/" }, { id: 2, url: "https://platform.claude.com/", discarded: true }], false],
    [[{ id: 1, url: "https://platform.claude.com/" }, { id: 2, url: "https://platform.claude.com/", status: "loading" }], false],
    [[], true],
  ]) {
    for (const outcome of ["ok", "signed_out"]) {
      const w = worker(async () => ({ status: outcome, payloads: [], failures: [] }), undefined, undefined,
        { tabs, executeScript: async () => [{ result: { status: 200, body: [] } }] });
      w.context.chrome.storage.local.get = async () => ({ cache: { other: cached } });
      if (failQuery) w.context.chrome.tabs.query = async () => { throw new Error("no tabs API"); };
      await vm.runInContext("poll()", w.context);
      assert.deepEqual(w.stored.at(-1).cache, { other: cached }, `${outcome} with ${JSON.stringify(tabs)}`);
      assert.notEqual(w.sessions.at(-1).status.console.state, "signed_out");
    }
  }
});

// A reader that reports what its session says: a 401 means signed out.
const sessionReader = async (read, now, cache, post, pools) => {
  const answer = await read("https://platform.claude.com/api/organizations");
  if (answer.status === 401) return { status: "signed_out", payloads: [] };
  pools.seen.add("main");
  pools.read.add("main");
  return { status: "ok", payloads: [{ name: "Main", balance: 100, currency: "USD" }], listed: ["main"], failures: [] };
};
const signedIn = async (url, options) => url.startsWith("http://127.0.0.1")
  ? { ok: true, status: 204 } : { ok: true, status: 200, json: async () => [] };

test("a signed-out container tab cannot hide the signed-in main session", async () => {
  const posts = [];
  const fetch = async (url, options) => { if (url.startsWith("http://127.0.0.1")) posts.push(url); return signedIn(url, options); };
  const w = worker(sessionReader, fetch, undefined, {
    tabs: [{ id: 1, url: "https://platform.claude.com/" }],
    executeScript: async () => [{ result: { status: 401, body: null } }],
  });
  const recent = { at: Date.now(), reconciledAt: Date.now(), daily: {} };
  w.context.chrome.storage.local.get = async () => ({ cache: { main: { ...recent }, container: recent } });
  await vm.runInContext("poll()", w.context);
  assert.equal(w.sessions.at(-1).status.console.state, "ok");
  assert.ok(!posts.some(url => url.endsWith("/v1/status")), "no global sign-out");
  assert.deepEqual(Object.keys(w.stored.at(-1).cache).sort(), ["container", "main"]);
  assert.equal(w.injections.length, 1);
});

test("a pool whose session is not open is kept for a week, then dropped", async () => {
  const day = 24 * 60 * 60 * 1000;
  const w = worker(sessionReader, signedIn);
  const entry = at => ({ at, reconciledAt: at, daily: {} });
  w.context.chrome.storage.local.get = async () => ({
    cache: { main: entry(Date.now()), closedTab: entry(Date.now() - 8 * day), gone: entry(Date.now() - 8 * day) },
    sessions: { closedTab: { seenAt: Date.now() - day, elsewhereAt: Date.now() - day } },
  });
  await vm.runInContext("poll()", w.context);
  const cache = w.stored.at(-1).cache;
  assert.deepEqual(Object.keys(cache).sort(), ["closedTab", "main"]);
  assert.ok(Date.now() - w.stored.at(-1).sessions.main.seenAt < day);
});

test("a tab that does not answer beside one that signs out is reported, not hidden", async () => {
  const posts = [];
  const fetch = async (url, options) => { if (url.startsWith("http://127.0.0.1")) posts.push(url); return signedIn(url, options); };
  const w = worker(sessionReader, fetch, undefined, {
    tabs: [{ id: 1, url: "https://platform.claude.com/" }, { id: 2, url: "https://platform.claude.com/" }],
    executeScript: async ({ target }) => {
      if (target.tabId === 1) throw new Error("frozen");
      return [{ result: { status: 401, body: null } }];
    },
  });
  await vm.runInContext("poll()", w.context);
  const { console: credits } = w.sessions.at(-1).status;
  assert.equal(credits.state, "ok");
  assert.equal(credits.unchecked, 1);
  assert.ok(!posts.some(url => url.endsWith("/v1/status")), "no global sign-out");
  assert.deepEqual(w.injections.map(injection => injection.target.tabId), [1, 2]);
});

test("a signed-out main session does not sign out a container whose tab is closed", async () => {
  const posts = [];
  const signedOut = async (url, options) => {
    if (url.startsWith("http://127.0.0.1")) { posts.push(url); return { ok: true, status: 204 }; }
    return { ok: false, status: 401 };
  };
  const recent = { at: Date.now(), reconciledAt: Date.now(), daily: {} };
  const minuteAgo = Date.now() - 60000;
  // A container listed its pool a minute ago; the main session's pool is its own.
  let w = worker(sessionReader, signedOut);
  w.context.chrome.storage.local.get = async () => ({ cache: { container: { ...recent }, main: { ...recent } },
    sessions: { container: { seenAt: minuteAgo, elsewhereAt: minuteAgo }, main: { seenAt: minuteAgo } } });
  await vm.runInContext("poll()", w.context);
  assert.equal(w.sessions.at(-1).status.console.state, "signed_out");
  assert.ok(!posts.some(url => url.endsWith("/v1/status")), "no global sign-out while a container is unknown");
  assert.deepEqual(Object.keys(w.stored.at(-1).cache), ["container"]);

  // With only main-session pools, the sign-out is verified and sent.
  posts.length = 0;
  w = worker(sessionReader, signedOut);
  w.context.chrome.storage.local.get = async () => ({ cache: { main: { ...recent } }, sessions: { main: { seenAt: minuteAgo } } });
  await vm.runInContext("poll()", w.context);
  assert.ok(posts.some(url => url.endsWith("/v1/status")));
  assert.deepEqual(w.stored.at(-1).cache, {});
});

// teamReader models two accounts that share a team: the main session lists
// the team and a personal pool; the container lists the team and its own.
const teamReader = async (read, now, cache, post, pools) => {
  const answer = await read("https://platform.claude.com/api/organizations");
  if (answer.status === 401) return { status: "signed_out", payloads: [] };
  const listed = answer.body?.container ? ["team", "container"] : ["team", "personal"];
  for (const pool of listed) {
    if (pools.read.has(pool)) continue;
    pools.seen.add(pool);
    pools.read.add(pool);
  }
  return { status: "ok", payloads: [], listed, failures: [] };
};

test("a team pool shared with a container survives the container closing and the main session signing out", async () => {
  const posts = [];
  let mainSignedIn = true;
  const fetch = async url => {
    if (url.startsWith("http://127.0.0.1")) { posts.push(url); return { ok: true, status: 204 }; }
    return mainSignedIn ? { ok: true, status: 200, json: async () => ({}) } : { ok: false, status: 401 };
  };
  const recent = { at: Date.now(), reconciledAt: Date.now(), daily: {} };
  let cache = { team: { ...recent }, personal: { ...recent }, container: { ...recent } };
  let sessions = {};
  // Poll 1: main session and an open container tab, both reading the team.
  let w = worker(teamReader, fetch, undefined, {
    tabs: [{ id: 1, url: "https://platform.claude.com/" }],
    executeScript: async () => [{ result: { status: 200, body: { container: true } } }],
  });
  w.context.chrome.storage.local.get = async () => ({ cache, sessions });
  await vm.runInContext("poll()", w.context);
  ({ cache, sessions } = w.stored.at(-1));
  assert.ok(Number.isFinite(sessions.team.elsewhereAt), "the container's listing of the team is recorded");
  assert.ok(!Number.isFinite(sessions.personal.elsewhereAt));

  // Poll 2: the container tab is closed and the main session signs out.
  mainSignedIn = false;
  w = worker(teamReader, fetch);
  w.context.chrome.storage.local.get = async () => ({ cache, sessions });
  await vm.runInContext("poll()", w.context);
  assert.deepEqual(Object.keys(w.stored.at(-1).cache).sort(), ["container", "team"]);
  assert.ok(!posts.some(url => url.endsWith("/v1/status")), "no global sign-out");
});

test("a cost refresh and a failed container read still record the container's pools", async () => {
  const posts = [];
  let mainSignedIn = true;
  const fetch = async url => {
    if (url.startsWith("http://127.0.0.1")) { posts.push(url); return { ok: true, status: 204 }; }
    return mainSignedIn ? { ok: true, status: 200, json: async () => ({}) } : { ok: false, status: 401 };
  };
  // The main session lists the team and refreshes its costs, which rewrites
  // the cache entry. The container lists the team and a pool whose read
  // fails, so its reader throws.
  const reader = async (read, now, cache, post, pools) => {
    const answer = await read("https://platform.claude.com/api/organizations");
    if (answer.status === 401) return { status: "signed_out", payloads: [] };
    if (answer.body?.container) {
      const error = new Error("Claude Console request failed");
      error.listed = ["team", "broken"];
      throw error;
    }
    cache.team = { at: Date.now(), reconciledAt: Date.now(), daily: {} };
    pools.seen.add("team");
    pools.read.add("team");
    return { status: "ok", payloads: [{ name: "Team", balance: 1, currency: "USD" }], listed: ["team", "personal"], failures: [] };
  };
  let w = worker(reader, fetch, undefined, {
    tabs: [{ id: 1, url: "https://platform.claude.com/" }],
    executeScript: async () => [{ result: { status: 200, body: { container: true } } }],
  });
  await vm.runInContext("poll()", w.context);
  let { cache, sessions } = w.stored.at(-1);
  assert.ok(Number.isFinite(sessions.team.elsewhereAt));
  // A second poll refreshes the team's costs with the container closed.
  w = worker(reader, fetch);
  w.context.chrome.storage.local.get = async () => ({ cache, sessions });
  await vm.runInContext("poll()", w.context);
  ({ cache, sessions } = w.stored.at(-1));
  assert.ok(Number.isFinite(sessions.team.elsewhereAt), "a cost refresh keeps the container's record");
  // Then the main session signs out: the team stays, and no sign-out is sent.
  mainSignedIn = false;
  w = worker(reader, fetch);
  w.context.chrome.storage.local.get = async () => ({ cache, sessions });
  await vm.runInContext("poll()", w.context);
  assert.ok("team" in w.stored.at(-1).cache);
  assert.ok(!posts.some(url => url.endsWith("/v1/status")), "no global sign-out");
});

test("a pool a container lists but cannot read yet is remembered for a week", async () => {
  const posts = [];
  const fetch = async url => {
    if (url.startsWith("http://127.0.0.1")) { posts.push(url); return { ok: true, status: 204 }; }
    return { ok: false, status: 401 };
  };
  // The main session is signed out; the container lists a pool whose read fails.
  const reader = async read => {
    const answer = await read("https://platform.claude.com/api/organizations");
    if (answer.status === 401) return { status: "signed_out", payloads: [] };
    const error = new Error("Claude Console request failed");
    error.listed = ["fresh"];
    throw error;
  };
  let w = worker(reader, fetch, undefined, {
    tabs: [{ id: 1, url: "https://platform.claude.com/" }],
    executeScript: async () => [{ result: { status: 200, body: [] } }],
  });
  await vm.runInContext("poll()", w.context);
  let { cache, sessions } = w.stored.at(-1);
  assert.ok(Number.isFinite(sessions.fresh?.elsewhereAt), "the listing is recorded without a cost entry");
  // The container closes: no global sign-out while its pool is remembered.
  w = worker(reader, fetch);
  w.context.chrome.storage.local.get = async () => ({ cache, sessions });
  await vm.runInContext("poll()", w.context);
  assert.ok(!posts.some(url => url.endsWith("/v1/status")));
  // A week later the record expires and the sign-out is sent.
  const week = 8 * 24 * 60 * 60 * 1000;
  sessions = { fresh: { seenAt: Date.now() - week, elsewhereAt: Date.now() - week } };
  w = worker(reader, fetch);
  w.context.chrome.storage.local.get = async () => ({ cache: {}, sessions });
  await vm.runInContext("poll()", w.context);
  assert.deepEqual(w.stored.at(-1).sessions, {});
  assert.ok(posts.some(url => url.endsWith("/v1/status")));
});

test("the badge asks for a fix only when Clawmeter misses readings it could have", () => {
  const { context } = worker(async () => ({ status: "ok", payloads: [] }));
  const status = (claude, delivery = ["saved"]) => ({ delivery, claude });
  assert.equal(context.attention(status({ state: "ok", accounts: [] }), true), "");
  assert.equal(context.attention(status({ state: "ok", accounts: [] }, ["saved", "unreachable"]), false), "Clawmeter isn't running on this computer");
  assert.equal(context.attention(status({ state: "ok", accounts: [] }, ["rejected"]), false), "Update Clawmeter to accept readings");
  // A browser that has never read claude.ai is used only for API credits.
  assert.equal(context.attention(status({ state: "no_tab" }), false), "");
  assert.equal(context.attention(status({ state: "no_tab" }), true), "Clawmeter needs an open claude.ai tab");
  assert.equal(context.attention(status({ state: "signed_out" }), true), "Sign in to claude.ai for Clawmeter");
});

test("a poll sets the badge, and remembers that claude.ai was read", async () => {
  const w = worker(async () => ({ status: "ok", payloads: [] }), undefined,
    async () => ({ status: "ok", payloads: [], accounts: [{ name: "Max", resets: 1 }] }), { tabs: [{ id: 1 }],
      executeScript: async () => [{ result: { status: 200, body: {} } }] });
  await tick();
  await w.context.poll();
  assert.ok(w.stored.some(value => value.claudeRead === true));
  assert.deepEqual(w.badges.slice(-2), ["", "Clawmeter"]);
});

test("open-claude opens a pinned background tab and reads it once it has loaded", async () => {
  let reads = 0;
  const w = worker(async () => { reads++; return { status: "ok", payloads: [] }; });
  await tick();
  const replies = [];
  assert.equal(w.listeners.message("open-claude", { id: "self" }, value => replies.push(value)), true);
  await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(w.opened)), [{ url: "https://claude.ai/", pinned: true, active: false }]);
  const before = reads;
  w.listeners.updated(8, { status: "complete" });
  w.listeners.updated(7, { status: "loading" });
  await tick();
  assert.equal(reads, before);
  w.listeners.updated(7, { status: "complete" });
  for (let i = 0; i < 10 && !replies.length; i++) await tick();
  assert.equal(reads, before + 1);
  assert.deepEqual(replies, [true]);
  assert.equal(w.listeners.updated, null);
});

test("open-claude reads the new tab even when a check was already running", async () => {
  let reads = 0, release;
  const w = worker(async () => {
    reads++;
    if (reads === 1) await new Promise(resolve => { release = resolve; });
    return { status: "ok", payloads: [] };
  });
  await tick();
  void w.context.poll();
  await tick();
  assert.equal(reads, 1);
  const replies = [];
  w.listeners.message("open-claude", { id: "self" }, value => replies.push(value));
  await tick();
  w.listeners.updated(7, { status: "complete" });
  await tick();
  assert.equal(reads, 1);
  release();
  for (let i = 0; i < 20 && !replies.length; i++) await tick();
  assert.equal(reads, 2);
  assert.deepEqual(replies, [true]);
});
