"use strict";

importScripts("console.js");

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

async function poll() {
  if (inFlight) return;
  inFlight = true;
  let cache;
  try {
    const stored = await chrome.storage.local.get("cache");
    cache = stored.cache && typeof stored.cache === "object" && !Array.isArray(stored.cache) ? stored.cache : {};
    const result = await readConsole(async url => {
      return request(url, {
        method: "GET", credentials: "include", cache: "no-store",
        headers: { Accept: "application/json" },
      }, async response => ({ status: response.status, body: response.ok ? await response.json() : null }));
    }, Date.now(), cache);
    if (result.status === "signed_out") {
      cache = {};
      await post("/v1/status", { status: "signed_out" });
    } else {
      for (const payload of result.payloads) await post("/v1/api-credits", payload);
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
