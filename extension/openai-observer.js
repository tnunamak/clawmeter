"use strict";

// Observe only the billing page's own GETs. No credentials are read, replayed,
// or forwarded. Each offer is bound through discovery to an exact API org.
function createOpenAICollector({ emit, hash, now = Date.now }) {
  const accounts = new Map(), organizations = new Map();
  const id = value => typeof value === "string" && /^[A-Za-z0-9_-]{1,160}$/.test(value);
  const fresh = at => Number.isFinite(at) && at <= now() && now() - at < 300000;
  const name = value => typeof value === "string" && value.trim() === value && value.length > 0 && value.length <= 120 && !/[\x00-\x1f\x7f]/.test(value);
  function record(map, key) {
    if (!map.has(key)) { if (map.size >= 20) throw Error("Too many billing accounts"); map.set(key, {}); }
    return map.get(key);
  }
  async function send(org, section) {
    const state = record(organizations, org);
    await emit({ pool: await hash("clawmeter:openai-api-org:v1:" + org), name: state.name || "OpenAI organization", ...section });
  }
  async function offer(account) {
    if (!account.offer || !fresh(account.offerAt) || !fresh(account.discoveryAt)) return;
    for (const destination of account.destinations || []) {
      // The aggregate receipt is only as fresh as its oldest binding input.
      const at = Math.min(account.offerAt, account.discoveryAt);
      const benefit = { ...normalizeOpenAIBenefit(account.offer, at), link_state: "unknown" };
      await send(destination.org, { benefit });
    }
  }
  return async function observe({ url, method = "GET", organization, body, at }) {
    if (method.toUpperCase() !== "GET" || !fresh(at)) return;
    const u = new URL(url);
    if (u.origin === "https://api.openai.com" && u.pathname === "/v1/dashboard/billing/credit_grants") {
      if (!id(organization)) return;
      const state = record(organizations, organization);
      if (state.creditAt >= at) return;
      const credits = normalizeOpenAICredits(body, at);
      await send(organization, { credits }); state.creditAt = at;
      return;
    }
    if (u.origin !== "https://chatgpt.com") return;
    let match = u.pathname.match(/^\/backend-api\/product-account-links\/organizations\/([A-Za-z0-9_-]{1,160})$/);
    if (match) {
      if (!body || !Object.hasOwn(body, "link") || typeof body.can_link !== "boolean") return;
      const state = record(organizations, match[1]);
      if (state.linkAt >= at) return;
      const linked = body.link !== null && typeof body.link === "object" && !Array.isArray(body.link);
      const link = { state: linked ? "linked" : body.link === null && body.can_link ? "unlinked" : "unknown", observed_at: new Date(at).toISOString() };
      await send(match[1], { link }); state.linkAt = at;
      return;
    }
    match = u.pathname.match(/^\/backend-api\/product-account-links\/accounts\/([A-Za-z0-9_-]{1,160})\/benefits\/chatgpt_api_credits_promo$/);
    if (match && u.searchParams.get("destination_type") === "api_org") {
      normalizeOpenAIBenefit(body, at); // Reject before caching.
      const account = record(accounts, match[1]);
      if (account.offerAt >= at) return;
      account.offer = body; account.offerAt = at;
      await offer(account); return;
    }
    if (u.pathname !== "/backend-api/product-account-links/discovery" || u.searchParams.get("destination_type") !== "api_org") return;
    const accountID = u.searchParams.get("account_id");
    if (!id(accountID) || !Array.isArray(body?.destinations) || body.destinations.length > 20) return;
    const destinations = body.destinations.filter(d => d.destination_type === "api_org" && id(d.destination_account_id) && name(d.display_name))
      .map(d => ({ org: d.destination_account_id, name: d.display_name }));
    const account = record(accounts, accountID);
    if (account.discoveryAt >= at) return;
    account.destinations = destinations; account.discoveryAt = at;
    for (const d of destinations) record(organizations, d.org).name = d.name;
    await offer(account);
  };
}

function installOpenAIObserver(win) {
  if (win.location.origin !== "https://platform.openai.com" || win.__clawmeterOpenAIObserver) return;
  win.__clawmeterOpenAIObserver = true;
  let queue = Promise.resolve();
  const observe = createOpenAICollector({
    hash: async value => [...new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value)))].map(b => b.toString(16).padStart(2, "0")).join(""),
    emit: receipt => win.postMessage({ type: "clawmeter-openai-receipt", receipt }, win.location.origin),
  });
  const target = url => {
    try {
      const u = new URL(url, win.location.href);
      return (u.origin === "https://api.openai.com" && u.pathname === "/v1/dashboard/billing/credit_grants") ||
        (u.origin === "https://chatgpt.com" && (u.pathname === "/backend-api/product-account-links/discovery" ||
          /^\/backend-api\/product-account-links\/(organizations\/[A-Za-z0-9_-]+|accounts\/[A-Za-z0-9_-]+\/benefits\/chatgpt_api_credits_promo)$/.test(u.pathname)));
    } catch { return false; }
  };
  const capture = (info, read) => {
    // Read response clones in order; page fetches remain independent.
    queue = queue.then(async () => { const body = await read(); await observe({ ...info, body }); }).catch(() => {});
  };
  const originalFetch = win.fetch;
  win.fetch = function(input, init) {
    const url = typeof input === "string" || input instanceof URL ? String(input) : input.url;
    const method = init?.method || input?.method || "GET";
    let info;
    if (method.toUpperCase() === "GET" && target(url)) {
      // Read exactly one non-secret header. Never enumerate or export headers.
      const organization = new Headers(init?.headers || input?.headers).get("OpenAI-Organization");
      info = { url, method, organization, at: Date.now() };
    }
    return originalFetch.apply(this, arguments).then(response => {
      if (info && response.ok && response.url && target(response.url)) {
        const copy = response.clone();
        capture(info, async () => { const text = await copy.text(); if (text.length > 262144) throw Error("Oversized receipt"); return JSON.parse(text); });
      }
      return response;
    });
  };
  const proto = win.XMLHttpRequest.prototype, open = proto.open, header = proto.setRequestHeader;
  const requests = new WeakMap();
  proto.open = function(method, url) {
    const info = { method, url: new URL(url, win.location.href).href, at: Date.now() };
    requests.set(this, info);
    if (method.toUpperCase() === "GET" && target(info.url)) this.addEventListener("load", () => {
      if (requests.get(this) !== info) return;
      if (this.status < 200 || this.status >= 300 || !target(this.responseURL)) return;
      capture(info, async () => {
        if (this.responseType === "json") return this.response;
        if (this.responseText.length > 262144) throw Error("Oversized receipt");
        return JSON.parse(this.responseText);
      });
    }, { once: true });
    return open.apply(this, arguments);
  };
  proto.setRequestHeader = function(key, value) {
    if (String(key).toLowerCase() === "openai-organization") { const info = requests.get(this); if (info) info.organization = value; }
    return header.apply(this, arguments);
  };
}

if (typeof window !== "undefined") installOpenAIObserver(window);
if (typeof module !== "undefined" && module.exports) module.exports = { createOpenAICollector, installOpenAIObserver };
