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

async function readFromClaudeTab(url, fallback, sawTab = () => {}) {
  let tabs = [];
  try {
    tabs = await chrome.tabs.query({ url: "https://claude.ai/*", discarded: false, status: "complete" });
  } catch {}
  if (tabs.length) sawTab();
  for (const tab of tabs) {
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
  }
  return fallback(url);
}

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
  let tab = false;
  try {
    const result = await readResets(url => readFromClaudeTab(url, getJSON, () => { tab = true; }), Date.now,
      payload => send("/v1/resets", payload));
    status.claude = result.status === "signed_out" ? { state: tab ? "signed_out" : "no_tab" } :
      { state: "ok", accounts: result.accounts };
  } catch (error) {
    // Resets and credits are independent; a failure in one never blocks the other.
    status.claude = { state: tab ? "error" : "no_tab", error: String(error?.message || error) };
  }
  try {
    const stored = await chrome.storage.local.get("cache");
    cache = stored.cache && typeof stored.cache === "object" && !Array.isArray(stored.cache) ? stored.cache : {};
    const result = await readConsole(getJSON, Date.now, cache, payload => send("/v1/api-credits", payload));
    if (result.status === "signed_out") {
      cache = {};
      await send("/v1/status", { status: "signed_out" });
      status.console = { state: "signed_out" };
    } else {
      status.console = { state: "ok", failures: result.failures,
        pools: result.payloads.map(payload => ({ name: payload.name, balance: payload.balance, currency: payload.currency })) };
    }
  } catch (error) {
    // Failed polls never refresh the receiver's observation time.
    status.console = { state: "error", error: String(error?.message || error) };
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
