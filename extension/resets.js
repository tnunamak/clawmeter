"use strict";

const claudeOrigin = "https://claude.ai";
// The Console's plan renewal date changes monthly, so each organization's
// link state is read at most once an hour.
const linkStateTTL = 60 * 60 * 1000;

// readResets reads promotional reset grants for each claude.ai organization in
// the signed-in session and posts one payload per organization. The receiver
// keeps only organizations that match a Claude source set up in Clawmeter.
// A caller that reads several sessions passes one set of organizations
// already read; one that failed in a session can still be read by the next.
// links caches each organization's renewal lookup across polls.
async function readResets(getJSON, now, post = async () => {}, read = new Set(), links = new Map()) {
  const clock = typeof now === "function" ? now : () => now;
  const time = new Date(clock()).getTime();
  if (!Number.isFinite(time)) throw new Error("Invalid poll time");
  const changed = () => { throw new Error("Claude reset data changed format"); };
  const timestamp = value => typeof value === "string" && Number.isFinite(Date.parse(value));
  const request = async path => {
    const result = await getJSON(claudeOrigin + path);
    if (result.status < 200 || result.status >= 300) {
      const error = new Error("Claude request failed");
      error.status = result.status;
      throw error;
    }
    return result.body;
  };
  // The renewal date is extra: any failure or unexpected answer means none.
  const renewalOf = async (id, at) => {
    const cached = links.get(id);
    if (cached && at - cached.at < linkStateTTL) return cached.next;
    let next = null;
    try {
      const state = await request(`/api/quirky-lollipop/link-state?organization_uuid=${encodeURIComponent(id)}`);
      if (state?.status === "linked" && timestamp(state.next_grant_at)) next = new Date(Date.parse(state.next_grant_at)).toISOString();
    } catch {
      next = null;
    }
    links.set(id, { at, next });
    return next;
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
  const payloads = [];
  // Each organization's name and what Clawmeter did with its reading, for
  // the popup.
  const accounts = [];
  const seen = new Set();
  let failure = null;
  for (const organization of organizations) {
    const id = String(organization?.uuid || organization?.id || "");
    if (!id || seen.has(id) || read.has(id)) continue;
    seen.add(id);
    const name = String(organization?.name || "Claude").trim().slice(0, 120);
    // One organization's failure must not hide the others.
    try {
      let usage;
      try {
        usage = await request(`/api/organizations/${encodeURIComponent(id)}/usage?at_wall=1&skip_spend=1`);
      } catch (error) {
        // Organizations without a chat plan, such as API-only ones, have no usage.
        if (error.status === 403 || error.status === 404) continue;
        throw error;
      }
      const resets = usage?.cedar_ember;
      if (resets == null) continue;
      const offered = resets.eligible === false ? [] : resets.grants;
      if (!Array.isArray(offered) || offered.length > 100) changed();
      const grants = [];
      for (const grant of offered) {
        if (!Number.isInteger(grant?.resets_left) || grant.resets_left < 0 || (grant.resets_left > 0 &&
            (typeof grant.paused !== "boolean" || !timestamp(grant.starts_at) || !timestamp(grant.ends_at)))) changed();
        grants.push({ resets_left: grant.resets_left, starts_at: grant.starts_at, ends_at: grant.ends_at, paused: grant.paused });
      }
      // Date each organization by its own read; a slow poll must not age later ones.
      const observed = clock();
      const payload = { observed_at: new Date(observed).toISOString(), organization: id, grants };
      const nextCredit = await renewalOf(id, observed);
      if (nextCredit) payload.next_credit_at = nextCredit;
      const delivery = await post(payload);
      payloads.push(payload);
      read.add(id);
      // Count only resets usable now; the receiver gets every grant.
      const usable = grants.filter(grant => grant.resets_left > 0 && !grant.paused &&
        Date.parse(grant.starts_at) <= observed && observed < Date.parse(grant.ends_at));
      accounts.push({ name, resets: usable.reduce((sum, grant) => sum + grant.resets_left, 0), delivery });
    } catch (error) {
      failure ??= error;
      accounts.push({ name, error: String(error?.message || error) });
    }
  }
  // With no organization read, the whole poll failed.
  if (failure && !payloads.length) throw failure;
  return { status: "ok", payloads, accounts };
}

if (typeof module !== "undefined") module.exports = { readResets };
