(async () => {
  const say = message => alert(`Clawmeter: ${message}`);
  if (location.origin !== "https://claude.ai") return say("Open Claude Usage, then click the bookmark there.");
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
    let organization = organizations.find(item => String(item.uuid || item.id || "") === activeOrgID);
    if (!organization && organizations.length === 1) organization = organizations[0];
    const organizationID = organization && String(organization.uuid || organization.id || "");
    if (!organizationID) return say("Couldn't tell which Claude organization is active. Switch to it in Claude and try again.");

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
    say(stage === "local"
      ? "Clawmeter isn't waiting. Choose Check Claude resets in the tray, then click the bookmark again. If your browser asks about local network access, allow it."
      : "Claude didn't respond. Sign in and try again.");
  }
})();
