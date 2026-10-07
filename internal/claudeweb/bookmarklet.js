(async () => {
  const fail = message => alert(`Clawmeter: ${message}`);
  let stage = "Claude organization lookup";

  try {
    let activeOrgID = "";
    try {
      const activeOrgCookie = await window.cookieStore?.get("lastActiveOrg");
      activeOrgID = activeOrgCookie?.value || "";
    } catch {}
    if (!activeOrgID) {
      const match = document.cookie.match(/(?:^|;\s*)lastActiveOrg=([^;]+)/);
      activeOrgID = match ? decodeURIComponent(match[1]) : "";
    }

    const organizationsResponse = await fetch("/api/organizations", {
      credentials: "same-origin",
      cache: "no-store",
    });
    if (!organizationsResponse.ok) return fail(`Claude organization lookup failed (${organizationsResponse.status}).`);
    const organizationData = await organizationsResponse.json();
    const organizations = Array.isArray(organizationData)
      ? organizationData
      : Array.isArray(organizationData?.organizations) ? organizationData.organizations : [];
    let organization = organizations.find(item => String(item.uuid || item.id || "") === activeOrgID);
    if (!organization && organizations.length === 1) organization = organizations[0];
    if (!organization) {
      return fail("Could not identify the active Claude organization. Select it in Claude and retry.");
    }
    const organizationID = organization.uuid || organization.id;
    if (!organizationID) return fail("Claude returned an organization without an ID.");

    const usageURL = `/api/organizations/${encodeURIComponent(organizationID)}/usage?at_wall=1&skip_spend=1`;
    stage = "Claude usage request";
    const response = await fetch(usageURL, { credentials: "same-origin", cache: "no-store" });
    if (!response.ok) return fail(`Claude usage request failed (${response.status}).`);

    const payload = await response.json();
    const resetData = payload?.cedar_ember;
    if (resetData?.eligible !== true || !Array.isArray(resetData.grants)) {
      return fail("This account response did not include reset grant details.");
    }

    if (resetData.grants.length > 100) return fail("Claude returned more grants than Clawmeter can safely check.");
    const grants = [];
    for (const grant of resetData.grants) {
      if (!Number.isInteger(grant.resets_left) || grant.resets_left < 0) {
        return fail("Claude returned an unreadable reset count; no result was sent.");
      }
      if (grant.resets_left > 0 && (typeof grant.paused !== "boolean" ||
          Number.isNaN(Date.parse(grant.starts_at)) || Number.isNaN(Date.parse(grant.ends_at)))) {
        return fail("Claude returned a reset grant without clear start, pause, or expiry fields; no result was sent.");
      }
      grants.push({
        resets_left: grant.resets_left,
        starts_at: grant.starts_at,
        ends_at: grant.ends_at,
        paused: grant.paused,
      });
    }

    stage = "local Clawmeter handoff";
    const challengeResponse = await fetch("http://127.0.0.1:17343/challenge", {
      mode: "cors",
      cache: "no-store",
    });
    if (!challengeResponse.ok) return fail("Clawmeter is not waiting for a check. Start one from its tray menu.");

    const { nonce } = await challengeResponse.json();
    if (!nonce) return fail("Clawmeter did not provide a valid check token; no result was sent.");
    if (!window.confirm("Record this browser's reset inventory under the Claude source selected in Clawmeter? Confirm that this browser is signed into the matching Claude account.")) return;
    const result = await fetch("http://127.0.0.1:17343/result", {
      method: "POST",
      mode: "cors",
      headers: { "Content-Type": "text/plain" },
      body: JSON.stringify({ nonce, grants }),
    });
    if (!result.ok) return fail("Clawmeter rejected this result. Start a new check from the tray.");
    alert("Clawmeter recorded the Claude reset check under the selected source.");
  } catch (error) {
    const next = stage === "Claude organization lookup"
      ? "Confirm you are signed in to claude.ai and the active organization is available."
      : stage === "Claude usage request"
        ? "Claude's usage endpoint may have changed; no result was sent."
        : "Confirm Clawmeter says it is waiting, click within 2 minutes, and check claude.ai's local-network permission.";
    fail(`${stage} failed (${error?.name || "Error"}). ${next}`);
  }
})();
