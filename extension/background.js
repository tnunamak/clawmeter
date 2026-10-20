"use strict";

importScripts("console.js", "resets.js");

let running = null;

async function ensureAlarm() {
  if (!(await chrome.alarms.get("poll"))) {
    await chrome.alarms.create("poll", { periodInMinutes: 1 });
  }
}

async function request(url, options, read) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 20000);
  try {
    const response = await fetch(url, { ...options, redirect: "error", signal: controller.signal });
    return read ? await read(response) : response;
  } finally {
    clearTimeout(timeout);
  }
}

// post sends a reading to Clawmeter and returns what happened to it:
// "saved", "ignored" (Clawmeter does not track that account), "rejected",
// or "unreachable" (Clawmeter is not running).
async function post(path, body) {
  try {
    const response = await request("http://127.0.0.1:17344" + path, {
      method: "POST", credentials: "omit",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    if (response.status === 204) return "ignored";
    return response.ok ? "saved" : "rejected";
  } catch {
    return "unreachable";
  }
}

// claude.ai answers a background worker's request with 403, so read it from
// inside an open claude.ai tab, where the request carries the page's origin.
// The script runs in an isolated world, so the page cannot change it.
function readInPage(url) {
  return fetch(url, {
    credentials: "include", cache: "no-store", redirect: "error",
    headers: { Accept: "application/json" }, signal: AbortSignal.timeout(20000),
  })
    .then(async response => ({ status: response.status, body: response.ok ? await response.json() : null }));
}

class TabUnavailable extends Error {}

async function readInTab(tab, url) {
  try {
    // A frozen tab never runs the page-side timeout, so the worker bounds
    // the wait itself; otherwise one tab could stall every later poll.
    let timer;
    const deadline = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("Timeout")), 20000); });
    const [injection] = await Promise.race([
      chrome.scripting.executeScript({ target: { tabId: tab.id }, func: readInPage, args: [url] }),
      deadline,
    ]).finally(() => clearTimeout(timer));
    const result = injection?.result;
    if (result && Number.isInteger(result.status)) return result;
  } catch {}
  throw new TabUnavailable("Tab did not answer");
}

// readEverySession runs read once in each open tab of a site, because tabs
// in different browser containers can be signed in to different accounts.
// Ordinary tabs share one session; readers skip organizations already read.
// read also runs once in the worker, which has the browser's main session:
// first when mainSession is true, otherwise only when no tab answered (the
// worker cannot read claude.ai). Each result is read's result or { error };
// unavailable counts tabs that did not answer, whose sessions are unknown.
async function readEverySession(pattern, fallback, read, mainSession = false) {
  let tabs = [];
  let unavailable = 0;
  try {
    tabs = await chrome.tabs.query({ url: pattern });
  } catch {
    unavailable++;
  }
  // A discarded or loading tab is not woken; its session counts as unknown.
  const ready = tabs.filter(tab => !tab.discarded && tab.status === "complete");
  unavailable += tabs.length - ready.length;
  const results = [];
  const readMain = async () => {
    try {
      results.push(await read(fallback));
    } catch (error) {
      results.push({ error });
    }
  };
  if (mainSession) await readMain();
  for (const tab of ready) {
    try {
      results.push(await read(url => readInTab(tab, url)));
    } catch (error) {
      if (error instanceof TabUnavailable) unavailable++;
      else results.push({ error });
    }
  }
  if (!results.length) await readMain();
  return { tabs: tabs.length, unavailable, results };
}

const message = error => String(error?.message || error);

const unseenPoolAge = 7 * 24 * 60 * 60 * 1000;

// poll runs one check at a time; a caller that arrives during a check waits
// for that check.
function poll() {
  running ??= check().finally(() => { running = null; });
  return running;
}

async function check() {
  let cache;
  const getJSON = async url => request(url, {
    method: "GET", credentials: "include", cache: "no-store",
    headers: { Accept: "application/json" },
  }, async response => ({ status: response.status, body: response.ok ? await response.json() : null }));
  // The popup shows this. It lives in session storage, so organization names
  // never reach the disk.
  const status = { at: new Date().toISOString(), delivery: [], claude: null, console: null };
  const send = async (path, payload) => {
    const delivery = await post(path, payload);
    status.delivery.push(delivery);
    return delivery;
  };
  // An organization seen in one session is not read again in another.
  const resetOrganizations = new Set();
  const resets = await readEverySession("https://claude.ai/*", getJSON,
    read => readResets(read, Date.now, payload => send("/v1/resets", payload), resetOrganizations));
  const resetsRead = resets.results.filter(result => result.status === "ok");
  // Resets and credits are independent; a failure in one never blocks the other.
  if (resetsRead.length) {
    status.claude = { state: "ok", unchecked: resets.unavailable, accounts: [...resetsRead.flatMap(result => result.accounts),
      ...resets.results.filter(result => result.error).map(result => ({ name: "A claude.ai tab", error: message(result.error) }))] };
  } else if (!resets.tabs) {
    status.claude = { state: "no_tab" };
  } else if (resets.results.some(result => result.status === "signed_out")) {
    status.claude = { state: "signed_out" };
  } else {
    status.claude = { state: "error", error: message(resets.results[0].error) };
  }
  try {
    const stored = await chrome.storage.local.get("cache");
    cache = stored.cache && typeof stored.cache === "object" && !Array.isArray(stored.cache) ? stored.cache : {};
    const pools = { seen: new Set(), read: new Set() };
    // The main session is always read, so a signed-out container tab cannot
    // hide it.
    const credits = await readEverySession("https://platform.claude.com/*", getJSON,
      read => readConsole(read, Date.now, cache, payload => send("/v1/api-credits", payload), pools), true);
    const complete = !credits.unavailable && credits.results.every(result => !result.error);
    const read = credits.results.filter(result => result.status === "ok");
    // A pool can belong to a container whose tab is closed, so an unseen
    // pool is dropped only after a week unseen.
    const now = Date.now();
    for (const [pool, entry] of Object.entries(cache)) {
      if (pools.seen.has(pool)) entry.seenAt = now;
      else if (now - (Number.isFinite(entry?.seenAt) ? entry.seenAt : entry?.at ?? 0) > unseenPoolAge) delete cache[pool];
    }
    if (read.length) {
      status.console = { state: "ok", unchecked: credits.unavailable,
        failures: [...read.flatMap(result => result.failures),
          ...credits.results.filter(result => result.error).map(result => ({ name: "A Claude Console tab", error: message(result.error) }))],
        pools: read.flatMap(result => result.payloads)
          .map(payload => ({ name: payload.name, balance: payload.balance, currency: payload.currency })) };
    } else if (complete && credits.results.every(result => result.status === "signed_out")) {
      cache = {};
      await send("/v1/status", { status: "signed_out" });
      status.console = { state: "signed_out" };
    } else {
      throw credits.results.find(result => result.error)?.error || new Error("A Claude Console tab did not answer");
    }
  } catch (error) {
    // Failed polls never refresh the receiver's observation time.
    status.console = { state: "error", error: message(error) };
  } finally {
    try {
      if (cache) await chrome.storage.local.set({ cache });
      await chrome.storage.session.set({ status });
    } catch {}
  }
}

chrome.runtime.onMessage.addListener((message, sender, reply) => {
  if (sender.id !== chrome.runtime.id || message !== "poll") return;
  poll().then(() => reply(true), () => reply(false));
  return true;
});

chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name === "poll") void poll();
});
// Poll at once on install and browser start, not a minute later.
chrome.runtime.onInstalled.addListener(() => { void ensureAlarm().catch(() => {}); void poll(); });
chrome.runtime.onStartup.addListener(() => { void ensureAlarm().catch(() => {}); void poll(); });
void ensureAlarm().catch(() => {});
