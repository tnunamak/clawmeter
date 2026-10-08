(async () => {
  const say = message => alert(`Clawmeter: ${message}`);
  const onConsole = location.origin === "https://platform.claude.com";
  if (location.origin !== "https://claude.ai" && !onConsole) return say("Open Claude Usage or the Claude Console, then click the bookmark there.");
  const local = "http://127.0.0.1:17343";
  let stage = "claude";

  try {
    let activeOrgID = "";
    try {
      activeOrgID = (await window.cookieStore?.get("lastActiveOrg"))?.value || "";
    } catch {}
    if (!activeOrgID) {
      const match = document.cookie.match(/(?:^|;\s*)lastActiveOrg=([^;]+)/);
      activeOrgID = match ? decodeURIComponent(match[1]) : "";
    }

    const organizationsResponse = await fetch("/api/organizations", { credentials: "same-origin", cache: "no-store" });
    if (!organizationsResponse.ok) return say(`Claude didn't respond (${organizationsResponse.status}). Sign in and try again.`);
    const organizationData = await organizationsResponse.json();
    const organizations = Array.isArray(organizationData)
      ? organizationData
      : Array.isArray(organizationData?.organizations) ? organizationData.organizations : [];
    const candidates = onConsole ? organizations.filter(item => Array.isArray(item.capabilities) && item.capabilities.includes("api")) : organizations;
    let organization = candidates.find(item => String(item.uuid || item.id || "") === activeOrgID);
    if (!organization && candidates.length === 1) organization = candidates[0];
    const organizationID = organization && String(organization.uuid || organization.id || "");
    if (!organizationID) return say("Couldn't tell which Claude organization is active. Switch to it in Claude and try again.");

    if (onConsole) {
      const base = `/api/organizations/${encodeURIComponent(organizationID)}`;
      const read = async path => {
        const response = await fetch(path, { credentials: "same-origin", cache: "no-store" });
        if (!response.ok) throw new Error(`Claude Console didn't load (${response.status}). Try again.`);
        return response.json();
      };
      const changed = "Claude Console's credit data changed format. Nothing was sent.";
      const isAmount = value => Number.isInteger(value) && value >= 0;
      const isTime = value => typeof value === "string" && !Number.isNaN(Date.parse(value));
      let credits, spend, workspaces;
      try {
        [credits, spend, workspaces] = await Promise.all([read(`${base}/prepaid/credits`), read(`${base}/current_spend`), read(`/api/console/organizations/${encodeURIComponent(organizationID)}/workspaces`)]);
      } catch (error) {
        return say(error.message);
      }
      const tranches = [...(credits?.tranches || []), ...(credits?.promo_tranches || [])];
      const promotional = (credits?.promo_tranches || []).reduce((sum, tranche) => sum + tranche.remaining_amount_minor_units, 0);
      const balance = credits?.amount_without_scoped_credits + promotional;
      if (!isAmount(credits?.amount) || !isAmount(credits.amount_without_scoped_credits) || !isAmount(balance) || Math.abs(balance - credits.amount) > Math.max(5, credits.amount * 0.01) ||
          typeof credits.currency !== "string" || (credits.balance?.credits && credits.balance.credits.exponent !== 2) ||
          !isAmount(spend?.amount) || !isTime(spend.resets_at) || !Array.isArray(workspaces) || tranches.length > 50) return say(changed);
      const grants = [];
      for (const tranche of tranches) {
        if (!isAmount(tranche.granted_amount_minor_units) || !isAmount(tranche.remaining_amount_minor_units) || !isTime(tranche.granted_at) ||
            (tranche.expires_at != null && !isTime(tranche.expires_at))) return say(changed);
        grants.push({ name: String(tranche.name || "API credit").trim().slice(0, 120), granted: tranche.granted_amount_minor_units, remaining: tranche.remaining_amount_minor_units, granted_at: tranche.granted_at, expires_at: tranche.expires_at ?? null });
      }
      const day = offset => new Date(Date.now() + offset * 86400000).toISOString().slice(0, 10);
      const daily = {};
      const spaces = ["default", ...workspaces.map(workspace => String(workspace.id || "")).filter(id => id && id !== "default")];
      for (const space of spaces) {
        let costs;
        try {
          costs = await read(`${base}/workspaces/${encodeURIComponent(space)}/usage_cost?starting_on=${day(-30)}&ending_before=${day(1)}&group_by=api_key_id`);
        } catch (error) {
          return say(error.message);
        }
        if (!costs?.costs || typeof costs.costs !== "object" || Array.isArray(costs.costs)) return say(changed);
        for (const kind of ["costs", "web_search_costs", "code_execution_costs", "session_usage_costs"]) {
          for (const [date, rows] of Object.entries(costs?.[kind] || {})) {
            if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Array.isArray(rows)) return say(changed);
            for (const row of rows) {
              if (typeof row.total !== "number" || !Number.isFinite(row.total) || row.total < 0) return say(changed);
              daily[date] = (daily[date] || 0) + row.total;
            }
          }
        }
      }
      let plan = null;
      try {
        const state = await read(`/api/quirky-lollipop/organizations/${encodeURIComponent(organizationID)}/link-state`);
        const planOrganization = String(state?.link?.organization?.id || "");
        if (state?.status === "linked" && planOrganization && /^[a-z0-9_]{1,40}$/.test(String(state.plan || "")) && isAmount(state.monthly_credit_usd_cents)) {
          plan = { organization: planOrganization, plan: state.plan, monthly_credit: state.monthly_credit_usd_cents };
        }
      } catch {}
      const month = new Date().toISOString().slice(0, 8);
      const monthTotal = Object.entries(daily).filter(([date]) => date.startsWith(month)).reduce((sum, [, amount]) => sum + amount, 0);
      if (Math.abs(monthTotal - spend.amount) > Math.max(5, spend.amount * 0.1)) return say("Claude Console's cost and spend totals don't match. Nothing was sent. Try again in a few minutes.");

      stage = "local";
      const challenge = await fetch(`${local}/challenge`, { mode: "cors", cache: "no-store" });
      const { nonce } = await challenge.json();
      if (!challenge.ok || !nonce) throw new Error("no check");
      const hash = async text => Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text))), byte => byte.toString(16).padStart(2, "0")).join("");
      const pool = await hash(`clawmeter-api-pool\u0000${organizationID}`);
      const link = plan && { account: await hash(`clawmeter-claude-reset\u0000${nonce}\u0000${plan.organization}`), plan: plan.plan, monthly_credit: plan.monthly_credit };
      const result = await fetch(`${local}/credits`, {
        method: "POST",
        mode: "cors",
        headers: { "Content-Type": "text/plain" },
        body: JSON.stringify({ nonce, pool, name: String(organization.name || "Claude API").trim().slice(0, 120), currency: credits.currency.toUpperCase(), balance, grants, month_spend: spend.amount, month_resets_at: spend.resets_at, daily, link }),
      });
      const { message } = await result.json();
      return say(message || `Result rejected (${result.status}).`);
    }

    const response = await fetch(`/api/organizations/${encodeURIComponent(organizationID)}/usage?at_wall=1&skip_spend=1`, { credentials: "same-origin", cache: "no-store" });
    if (!response.ok) return say(`Claude usage didn't load (${response.status}). Try again.`);
    const resetData = (await response.json())?.cedar_ember;
    const offered = resetData?.eligible === false ? [] : resetData?.grants;
    if (!Array.isArray(offered) || offered.length > 100) return say("Claude's reset data changed format. Nothing was sent.");
    const grants = [];
    for (const grant of offered) {
      if (!Number.isInteger(grant.resets_left) || grant.resets_left < 0 || (grant.resets_left > 0 &&
          (typeof grant.paused !== "boolean" || Number.isNaN(Date.parse(grant.starts_at)) || Number.isNaN(Date.parse(grant.ends_at))))) {
        return say("Claude's reset data changed format. Nothing was sent.");
      }
      grants.push({ resets_left: grant.resets_left, starts_at: grant.starts_at, ends_at: grant.ends_at, paused: grant.paused });
    }

    stage = "local";
    const challenge = await fetch(`${local}/challenge`, { mode: "cors", cache: "no-store" });
    const { nonce } = await challenge.json();
    if (!challenge.ok || !nonce) throw new Error("no check");

    const digest = await crypto.subtle.digest("SHA-256",
      new TextEncoder().encode(`clawmeter-claude-reset\u0000${nonce}\u0000${organizationID}`));
    const account = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");

    const result = await fetch(`${local}/result`, {
      method: "POST",
      mode: "cors",
      headers: { "Content-Type": "text/plain" },
      body: JSON.stringify({ nonce, account, grants }),
    });
    const { message } = await result.json();
    say(message || `Result rejected (${result.status}).`);
  } catch {
    if (stage !== "local") return say("Claude didn't respond. Sign in and try again.");
    let access = "";
    for (const name of ["loopback-network", "local-network-access"]) {
      try { access = (await navigator.permissions.query({ name })).state; break; } catch {}
    }
    const site = onConsole ? "platform.claude.com" : "claude.ai";
    const action = onConsole ? "Check API credits" : "Check Claude resets";
    say(access === "denied"
      ? `Your browser is blocking ${site} from reaching Clawmeter. Open site settings (left of the address bar), allow local network access, then click the bookmark again.`
      : access === "prompt"
        ? `Your browser didn't let ${site} reach Clawmeter. Click the bookmark again and choose Allow when it asks about local network access.`
        : access === "granted"
          ? `Clawmeter isn't waiting. Choose ${action} in the tray, then click the bookmark again.`
          : `Couldn't reach Clawmeter. If your browser asks about local network access, allow it. Otherwise choose ${action} in the tray, then click the bookmark again.`);
  }
})();
