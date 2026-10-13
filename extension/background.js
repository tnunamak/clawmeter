"use strict";

importScripts("console.js", "resets.js");

let inFlight = false;

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

async function post(path, body) {
  try {
    await request("http://127.0.0.1:17344" + path, {
      method: "POST", credentials: "omit",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
  } catch {}
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

async function readFromClaudeTab(url, fallback) {
  let tabs = [];
  try {
    tabs = await chrome.tabs.query({ url: "https://claude.ai/*", discarded: false, status: "complete" });
  } catch {}
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

async function poll() {
  if (inFlight) return;
  inFlight = true;
  let cache;
  const getJSON = async url => request(url, {
    method: "GET", credentials: "include", cache: "no-store",
    headers: { Accept: "application/json" },
  }, async response => ({ status: response.status, body: response.ok ? await response.json() : null }));
  try {
    await readResets(url => readFromClaudeTab(url, getJSON), Date.now, payload => post("/v1/resets", payload));
  } catch {
    // Resets and credits are independent; a failure in one never blocks the other.
  }
  try {
    const stored = await chrome.storage.local.get("cache");
    cache = stored.cache && typeof stored.cache === "object" && !Array.isArray(stored.cache) ? stored.cache : {};
    const result = await readConsole(getJSON, Date.now, cache, payload => post("/v1/api-credits", payload));
    if (result.status === "signed_out") {
      cache = {};
      await post("/v1/status", { status: "signed_out" });
    }
  } catch {
    // Failed polls never refresh the receiver's observation time.
  } finally {
    try {
      if (cache) await chrome.storage.local.set({ cache });
    } catch {}
    inFlight = false;
  }
}

chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name === "poll") void poll();
});
// Poll at once on install and browser start, not a minute later.
chrome.runtime.onInstalled.addListener(() => { void ensureAlarm().catch(() => {}); void poll(); });
chrome.runtime.onStartup.addListener(() => { void ensureAlarm().catch(() => {}); void poll(); });
void ensureAlarm().catch(() => {});
