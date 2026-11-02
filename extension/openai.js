"use strict";

// Pure USD normalization. Round half away from zero exactly once to cents.
// Number inputs use their canonical decimal spelling; strings never use floats.
const OPENAI_MAX_CENTS = 100000000000n;
function oaFail() { throw new TypeError("Invalid OpenAI credit receipt"); }
function oaObject(v) { if (!v || typeof v !== "object" || Array.isArray(v)) oaFail(); return v; }
function oaDecimal(v, strings = false) {
  if (strings ? typeof v !== "string" : typeof v !== "number" || !Number.isFinite(v)) oaFail();
  const s = String(v);
  if (s.length > 80 || !(strings ? /^-?\d+(?:\.\d+)?$/ : /^-?\d+(?:\.\d+)?(?:e[+-]?\d+)?$/i).test(s)) oaFail();
  const [mantissa, exponent = "0"] = s.toLowerCase().split("e");
  const neg = mantissa[0] === "-";
  const [whole, frac = ""] = (neg ? mantissa.slice(1) : mantissa).split(".");
  let n = BigInt(whole + frac) * (neg ? -1n : 1n);
  let scale = frac.length - Number(exponent);
  if (scale < 0) { n *= 10n ** BigInt(-scale); scale = 0; }
  return {n, scale};
}
function oaRound({n, scale}, signed = false) {
  if (!signed && n < 0n) oaFail();
  const negative = n < 0n;
  const abs = negative ? -n : n;
  const divisor = 10n ** BigInt(scale);
  let cents = (abs * 100n * 2n + divisor) / (2n * divisor);
  if (negative) cents = -cents;
  if (cents > OPENAI_MAX_CENTS || cents < -OPENAI_MAX_CENTS) oaFail();
  return Number(cents);
}
function oaMoney(v, strings = false, signed = false) {
  return oaRound(oaDecimal(v, strings), signed);
}
function oaRemaining(granted, used) {
  const a = oaDecimal(granted), b = oaDecimal(used), scale = Math.max(a.scale, b.scale);
  return oaRound({n: a.n * 10n ** BigInt(scale - a.scale) - b.n * 10n ** BigInt(scale - b.scale), scale});
}
function oaDate(v, seconds = false) {
  let ms;
  if (seconds) {
    if (typeof v !== "number" || !Number.isFinite(v)) oaFail();
    ms = v * 1000;
  } else if (typeof v === "number" && Number.isFinite(v)) {
    ms = v;
  } else if (typeof v === "string" && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(v)) {
    ms = Date.parse(v);
    // Reject calendar overflow accepted by Date.parse (e.g. February 30).
    const y = +v.slice(0, 4), m = +v.slice(5, 7), d = +v.slice(8, 10);
    if (m < 1 || m > 12 || d < 1 || d > new Date(Date.UTC(y, m, 0)).getUTCDate() ||
        +v.slice(11, 13) > 23 || +v.slice(14, 16) > 59 || +v.slice(17, 19) > 59) oaFail();
  } else oaFail();
  if (!Number.isFinite(ms) || Math.abs(ms) > 8640000000000000) oaFail();
  const date = new Date(ms);
  if (!Number.isFinite(date.getTime())) oaFail();
  return date.toISOString();
}
function normalizeOpenAICredits(body, observedAt) {
  oaObject(body);
  const observed = oaDate(observedAt);
  if (body.object !== "credit_summary") oaFail();
  const granted = oaMoney(body.total_granted), used = oaMoney(body.total_used);
  const balance = oaMoney(body.total_available, false, true);
  // Independent rounding of three provider totals permits at most one cent discrepancy.
  if (Math.abs(granted - used - balance) > 1) oaFail();
  const list = oaObject(body.grants);
  if (list.object !== "list" || !Array.isArray(list.data) || list.data.length > 200 ||
      (list.has_more !== undefined && list.has_more !== false) ||
      (body.has_more !== undefined && body.has_more !== false)) oaFail();
  const seen = new Set(), grants = [];
  for (const g of list.data) {
    oaObject(g);
    if (g.object !== "credit_summary" || typeof g.id !== "string" || !g.id.trim() ||
        g.id.length > 256 || seen.has(g.id) || typeof g.is_scheduled !== "boolean" ||
        typeof g.is_held_for_prepaid_transition !== "boolean") oaFail();
    seen.add(g.id);
    const amount = oaMoney(g.grant_amount), spent = oaMoney(g.used_amount);
    if (g.used_amount > g.grant_amount) oaFail();
    const start = oaDate(g.effective_at, true);
    if (g.expires_at === undefined) oaFail();
    const expiry = g.expires_at === null ? null : oaDate(g.expires_at, true);
    if (expiry !== null && Date.parse(expiry) <= Date.parse(start)) oaFail();
    // The provider's per-grant accounting is authoritative; do not subtract rounded amounts.
    const remaining = oaRemaining(g.grant_amount, g.used_amount);
    if (remaining > amount || spent > amount) oaFail();
    if (g.is_scheduled || g.is_held_for_prepaid_transition ||
        Date.parse(start) > Date.parse(observed) ||
        (expiry !== null && Date.parse(expiry) <= Date.parse(observed))) continue;
    const item = {name: "API credit", granted: amount, remaining, granted_at: start};
    if (expiry !== null) item.expires_at = expiry;
    grants.push(item);
  }
  const result = {currency: "USD", balance, observed_at: observed, grants};
  if (body.total_paid_available !== undefined) result.paid_balance = oaMoney(body.total_paid_available, false, true);
  return result;
}
function normalizeOpenAIBenefit(body, observedAt) {
  oaObject(body);
  const observed = oaDate(observedAt);
  if (typeof body.eligible !== "boolean") oaFail();
  if (!body.eligible) return {eligible: false, observed_at: observed};
  const offer = oaObject(body.offer);
  if (offer.currency !== "USD" || !["monthly", "annual"].includes(offer.cadence)) oaFail();
  const period = oaMoney(offer.period_credit_amount_usd, true);
  const claimable = oaMoney(offer.claimable_credit_amount_usd, true);
  if (period <= 0 || claimable > period) oaFail();
  return {eligible: true, currency: "USD", cadence: offer.cadence, period_credit: period,
    claimable_credit: claimable, expires_at: oaDate(offer.expires_at), observed_at: observed};
}
if (typeof module !== "undefined" && module.exports) {
  module.exports = {normalizeOpenAICredits, normalizeOpenAIBenefit};
}
