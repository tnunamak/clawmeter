"use strict";

const consoleOrigin = "https://platform.claude.com";
const usageInterval = 30 * 60 * 1000;
// Spend moved since the cached costs were read: refetch them, but not more
// often than this.
const usageRetry = 5 * 60 * 1000;

async function readConsole(getJSON, now, cache) {
  const time = new Date(now).getTime();
  if (!Number.isFinite(time) || !cache || typeof cache !== "object") throw new Error("Invalid poll state");
  const changed = () => { throw new Error("Claude Console credit data changed format"); };
  const object = value => value !== null && typeof value === "object" && !Array.isArray(value);
  const amount = value => Number.isSafeInteger(value) && value >= 0;
  const timestamp = value => typeof value === "string" && Number.isFinite(Date.parse(value));
  const request = async path => {
    const result = await getJSON(consoleOrigin + path);
    if (result.status < 200 || result.status >= 300) {
      const error = new Error("Claude Console request failed");
      error.status = result.status;
      throw error;
    }
    return result.body;
  };
  let data;
  try {
    data = await request("/api/organizations");
  } catch (error) {
    if (error.status === 401 || error.status === 403) return { status: "signed_out", payloads: [] };
    throw error;
  }
  const organizations = Array.isArray(data) ? data : data?.organizations;
  if (!Array.isArray(organizations)) changed();
  const hash = async value => {
    const crypto = globalThis.crypto || require("node:crypto").webcrypto;
    return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value))),
      byte => byte.toString(16).padStart(2, "0")).join("");
  };
  const day = value => new Date(value).toISOString().slice(0, 10);
  const month = day(time).slice(0, 8);
  const monthStart = Date.parse(month + "01T00:00:00Z");
  const startingOn = day(Math.min(monthStart, time - 6 * 86400000));
  const endingBefore = day(time + 86400000);
  const payloads = [];
  const seen = new Set();
  for (const organization of organizations) {
    if (!object(organization) || !Array.isArray(organization.capabilities)) changed();
    if (!organization.capabilities.includes("api")) continue;
    const id = String(organization.uuid || organization.id || "");
    if (!id) changed();
    const pool = await hash("clawmeter-api-pool\u0000" + id);
    if (seen.has(pool)) continue;
    seen.add(pool);
    const base = "/api/organizations/" + encodeURIComponent(id);
    let credits, spend;
    try {
      [credits, spend] = await Promise.all([request(base + "/prepaid/credits"), request(base + "/current_spend")]);
    } catch (error) {
      if (error.status === 403) continue;
      throw error;
    }
    if (!object(credits) || (credits.tranches != null && !Array.isArray(credits.tranches)) ||
        (credits.promo_tranches != null && !Array.isArray(credits.promo_tranches))) changed();
    const tranches = [...(credits.tranches || []), ...(credits.promo_tranches || [])];
    const promotional = (credits.promo_tranches || []).reduce((sum, tranche) => sum + tranche.remaining_amount_minor_units, 0);
    const balance = credits.amount_without_scoped_credits + promotional;
    if (!amount(credits.amount) || !amount(credits.amount_without_scoped_credits) || !amount(balance) ||
        Math.abs(balance - credits.amount) > Math.max(5, credits.amount * 0.01) ||
        typeof credits.currency !== "string" || (credits.balance?.credits && credits.balance.credits.exponent !== 2) ||
        !amount(spend?.amount) || !timestamp(spend.resets_at) || tranches.length > 50) changed();
    const grants = tranches.map(tranche => {
      if (!amount(tranche.granted_amount_minor_units) || !amount(tranche.remaining_amount_minor_units) ||
          tranche.remaining_amount_minor_units > tranche.granted_amount_minor_units || !timestamp(tranche.granted_at) ||
          (tranche.expires_at != null && !timestamp(tranche.expires_at))) changed();
      return { name: String(tranche.name || "API credit").trim().slice(0, 120),
        granted: tranche.granted_amount_minor_units, remaining: tranche.remaining_amount_minor_units,
        granted_at: tranche.granted_at, expires_at: tranche.expires_at ?? null };
    });
    let link = null;
    try {
      const state = await request("/api/quirky-lollipop/organizations/" + encodeURIComponent(id) + "/link-state");
      const planOrganization = String(state?.link?.organization?.id || "");
      if (state?.status === "linked" && planOrganization && /^[a-z0-9_]{1,40}$/.test(String(state.plan || "")) &&
          amount(state.monthly_credit_usd_cents)) {
        link = { organization: planOrganization, plan: state.plan, monthly_credit: state.monthly_credit_usd_cents };
      }
    } catch {}
    const readCosts = async () => {
      const workspaces = await request("/api/console/organizations/" + encodeURIComponent(id) + "/workspaces");
      if (!Array.isArray(workspaces)) changed();
      const spaces = new Set(["default", ...workspaces.map(workspace => String(workspace.id || "")).filter(Boolean)]);
      const daily = {};
      for (const space of spaces) {
        const costs = await request(base + "/workspaces/" + encodeURIComponent(space) +
          "/usage_cost?starting_on=" + startingOn + "&ending_before=" + endingBefore + "&group_by=api_key_id");
        if (!object(costs?.costs)) changed();
        for (const kind of ["costs", "web_search_costs", "code_execution_costs", "session_usage_costs"]) {
          if (costs[kind] != null && !object(costs[kind])) changed();
          for (const [date, rows] of Object.entries(costs[kind] || {})) {
            if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Array.isArray(rows)) changed();
            for (const row of rows) {
              if (typeof row.total !== "number" || !Number.isFinite(row.total) || row.total < 0) changed();
              daily[date] = (daily[date] || 0) + row.total;
            }
          }
        }
      }
      return daily;
    };
    // Costs reconcile when this month's daily total matches current spend.
    const reconciles = daily => object(daily) && Object.entries(daily).every(([date, value]) =>
      /^\d{4}-\d{2}-\d{2}$/.test(date) && typeof value === "number" && Number.isFinite(value) && value >= 0) &&
      Math.abs(Object.entries(daily).filter(([date]) => date.startsWith(month))
        .reduce((sum, [, value]) => sum + value, 0) - spend.amount) <= Math.max(5, spend.amount * 0.1);
    let entry = cache[pool];
    const age = entry && Number.isFinite(entry.at) && entry.at <= time ? time - entry.at : Infinity;
    if (age >= usageInterval || (!reconciles(entry.daily) && age >= usageRetry)) {
      const daily = await readCosts();
      if (reconciles(daily)) {
        entry = { at: time, daily };
      } else if (entry) {
        // Cost reports can lag spend by minutes. Keep the last costs that
        // matched, so only the pace lags; the balance and spend stay current.
        entry = { ...entry, at: time };
      } else {
        continue;
      }
      // Only hashed pool IDs and aggregate costs survive worker restarts.
      cache[pool] = entry;
    }
    payloads.push({ pool, name: String(organization.name || "Claude API").trim().slice(0, 120),
      currency: credits.currency.toUpperCase(), balance, grants, month_spend: spend.amount,
      month_resets_at: spend.resets_at, daily: entry.daily, link });
  }
  for (const pool of Object.keys(cache)) {
    if (!seen.has(pool)) delete cache[pool];
  }
  return { status: "ok", payloads };
}

globalThis.readConsole = readConsole;
if (typeof module !== "undefined") module.exports = { readConsole };
