# Claude Reset Grants: Web, OAuth, and Claude Code

Date: 2026-09-24 (follow-up research; initial observations from 2026-09-23)

## Question

Can Clawmeter read the expiring “Reset for free” grant shown in Claude web usage
settings with its existing Claude Code OAuth credential, or is the grant only
available through a Claude web session?

## Findings

- Anthropic's Help Center says an account reset can refresh either a five-hour
  or weekly limit, while the normal weekly reset schedule remains unchanged.
  It says the “Reset for free” action is available on Claude web and Desktop,
  but not in Claude Code terminal or IDE. Usage is shared across surfaces.
- The installed Claude Code 2.1.280 binary contains the hidden `/limit-reset`
  command, `cedar_ember` and `juniper_tide` identifiers, reset-grant field names,
  and the request path `/api/oauth/usage?cedar_ember=1&skip_spend=1`. This is
  static binary evidence only: the command was not invoked because it can use a
  reset, and binary strings do not prove the current account is eligible.
- Anthropic's public Claude Code command reference documents `/usage` and
  `/rate-limit-options`, but does not list `/limit-reset` as a supported command.
- A non-CodexBar implementation report in `oh-my-pi` documents a live web-visible
  grant alongside OAuth usage responses. It reports the Cedar query returning
  `eligible: false` with `ineligible_reason: "surface"` for that account. This is
  community evidence, not Anthropic's published contract, but directly matches
  the web-vs-OAuth distinction relevant to Clawmeter. Its maintainer merged a
  fix for a separate parsing bug: a base usage response with a `null` program
  block must trigger the Cedar probe instead of being treated as an authoritative
  empty inventory. The fix exposes the server's `surface` reason; it does not
  bypass that server decision or redeem the web grant.
- The independent `claude-reset` project uses a dedicated browser login to read
  Claude web usage using the `sessionKey` cookie and `claude.ai` private usage
  endpoints. This proves there is a community-maintained browser-authenticated
  route for usage/reset-time monitoring, but its documented dashboard covers
  utilization and reset times, not banked promotional reset inventory. It does
  not establish that the browser route can safely expose the grant's expiry or
  target limits.
- Inspection of that project's `src/claudeClient.ts` narrows the evidence:
  it calls `GET /api/organizations/{org}/usage`, checks the `five_hour` and
  `seven_day` utilization/reset fields, and rejects unexpected response shape.
  Its README's browser login stores the session cookie in a local config file
  with owner-only permissions. This is a working precedent for browser-session
  usage polling, but not a ready-made solution for Clawmeter: it retains a
  password-equivalent cookie, uses an undocumented private endpoint, and does
  not parse reset grants.
- The reporter's merged `oh-my-pi` fix offers a useful parser lesson: `null`
  in the ordinary OAuth usage payload means “not evaluated,” not “no grant.” A
  separate read-only program probe is needed to get an authoritative inventory
  or a specific ineligibility reason. It still cannot reveal grants the service
  gates to another surface.
- Community discussion around the Opus 5.5 reset values user control: users
  appreciate being able to hold the reset rather than have usage reset
  automatically, and ask whether it applies to weekly versus session limits and
  whether the weekly reset date moves. Anthropic's current help article answers
  the latter two at the product level; this supports showing exact grant scope
  and expiry as facts, not recommending when to redeem it.
- Clawmeter's local `claude --json` refresh on 2026-09-23 returned fresh normal
  quota windows for both configured Claude sources, but no reset-credit field.
  A follow-up, sanitized read-only request to the Cedar OAuth URL returned HTTP
  200 with `eligible: false`, `ineligible_reason: "surface"`, `at_limit: false`,
  and an empty grants list. No account identifiers, raw response, or credential
  values were retained.
  A read-only request to the `claude.ai` organization-usage endpoint with the
  OAuth bearer returned HTTP 403 HTML. A later replay attempt using a cookie
  pasted into the conversation also returned HTTP 403 HTML; it did not establish
  whether that cookie would work in its original browser context.

## Conclusion

Claude Code does use its OAuth usage route for Cedar-related reset logic, so the
OAuth token is not categorically incapable of seeing reset metadata. However,
the server makes a per-surface eligibility decision. The web-visible promotional
grant is not guaranteed to be visible through OAuth; current community evidence
shows an explicit `surface` denial, and Clawmeter's own live response contained
no reset metadata. The direct Claude web endpoint remains a distinct cookie-
authenticated surface. Do not ship a claim that existing OAuth credentials can
read every web-visible grant until a live eligible-account response proves it.

## Product Implication

The user's browser capture is stronger than the failed server-side cookie replay:
the real `claude.ai/api/organizations/{org}/usage?at_wall=1&skip_spend=1`
request returned a response containing Cedar grant metadata, while the OAuth
route returned `ineligible_reason: "surface"`. This is direct evidence that
the web application's existing read response can expose the valuable count,
expiry, and cleared-limit fields for this account. The failed replay only shows
that a copied-cookie request from a non-browser context was rejected; it does
not show that the browser endpoint lacks the data.

The next feasibility test should therefore read the response inside the user's
already-authenticated browser context, without exporting authentication
credentials. Community implementations resolve the active org through
`GET /api/organizations` plus the `lastActiveOrg` selector, then call the same-
origin usage route. Clawmeter's local trial follows that pattern, reading only
the active-org selector in the page and sending only each available reset's
expiry to a one-shot loopback listener. The org ID and full response stay in the
browser. The local snapshot is stored per selected Claude source and shown under that
Claude account using Clawmeter's shared reset-credit model and renderers. The
user selects the source and confirms the browser account match, because
Clawmeter cannot verify it. It never invokes `/limit-reset` or a consume route.
(Superseded design: an earlier iteration showed the observation as a separate
`Claude Web` source; that was removed.)

This route is promising, not yet production-ready. It relies on an undocumented
web endpoint, Cloudflare/browser behavior, and a browser integration that adds
packaging and permissions. `claude-reset` is evidence that browser-session usage
polling is practical, but its source stores a session cookie and only validates
ordinary quota windows; it does not solve reset-grant retrieval or provide the
credential-handling design Clawmeter should adopt.

An alternative to an extension may exist for Chrome. Google's current Chrome
DevTools documentation describes an opt-in remote-debugging connection to an
active Chrome session (Chrome 144+): the user enables remote debugging in
`chrome://inspect`, and Chrome asks for permission when a client connects. This
could let Clawmeter evaluate the read-only usage request in an already logged-in
tab without extracting cookies or installing an extension. It is a powerful
browser-control capability, so Clawmeter would need to request it only after an
explicit user action, target only the Claude tab, make only the GET, and discard
the debugging connection immediately. Chrome 136 also blocks the older command-
line debugging switches against the default profile, specifically to hinder
cookie theft; do not work around that by attaching to a default profile with
legacy flags. This is a Chrome-specific lead, not yet validated against
Clawmeter's Go app, and does not establish equivalent support for Firefox,
Safari, or older Chrome versions. Chrome's published agent flow uses a consent
dialog on connection, which may add friction to every refresh.

## Developer Workflow Evidence

This is qualitative evidence, not a representative survey:

- Chrome now documents attaching AI agents to the user's active browser as a
  supported workflow. Its stated value is reusing tabs, live state, and existing
  authenticated sessions so the user does not repeat complex SSO/login flows.
  The same docs warn that the agent can access all profile data, including
  cookies and local/session storage, and require opt-in plus per-connection
  approval.
- Chrome DevTools MCP issue #825 requests persistent approval because the
  consent dialog appears on every connection. It is closed as not planned.
  This is direct evidence that repeat prompts are meaningful friction, and
  that Chrome intentionally keeps the security boundary rather than optimizing
  for unattended attachment.
- In an authenticated-pages discussion, developers describe both sides: a
  fresh automation profile means repeated login/2FA, while attaching to a real
  profile exposes more session authority than some developers want. A browser
  extension is offered as an alternative that works inside the existing login
  session.
- A community browser-bridge launch similarly emphasizes access to the user's
  everyday profile and avoiding re-login; a commenter explicitly asks whether
  it avoids repeatedly approving Chrome's debugging prompt. This is a small,
  self-selected sample, but it reinforces that friction is part of the
  decision, not a detail to wave away.

Implication for Clawmeter: the existing-profile experience is attractive, but
Chrome's remote-debugging consent is too broad for a background tray poller.
Keeping a full CDP connection alive avoids repeat prompts only by leaving a
powerful debugger attached to the user's whole profile. Reconnecting for each
manual reset check is safer but incurs a prompt each time. If an extension is
ruled out, the defensible Chrome-only fallback is an explicit, user-triggered
one-shot check with Chrome consent and a visible freshness timestamp; do not
present it as automatic, always-current tray data. A narrower browser-granted,
site-scoped API would be a better long-term fit if browsers expose one.

Do not invoke `/limit-reset` to probe eligibility: it is an action and may
consume a grant. Do not call any endpoint whose path contains `/consume`.

## Sources

- Anthropic Help Center, “What is a limit reset?”
  https://support.claude.com/en/articles/17007452-what-is-a-limit-reset
- Claude Code Docs, “Commands”
  https://code.claude.com/docs/en/commands
- `oh-my-pi` issue #12883, OAuth Cedar query and `surface` ineligibility report
  https://github.com/can1357/oh-my-pi/issues/12883
- `oh-my-pi` PR #12886, merged fix to probe `null` Cedar/Juniper blocks and
  preserve server eligibility reasons
  https://github.com/can1357/oh-my-pi/pull/12886
- `claude-reset`, community browser-authenticated Claude usage monitor; docs
  describe cookie-based browser login and usage dashboards, not promo-reset
  inventory support
  https://github.com/nazarli-shabnam/claude-reset
- `claude-reset` read-only web usage client implementation
  https://github.com/nazarli-shabnam/claude-reset/blob/main/src/claudeClient.ts
- Community implementations using same-origin organization discovery and
  active-org selection:
  https://github.com/HanChangHun/claude-usage
  https://github.com/alisalive/claude-usage-badge
- Chrome for Developers, changes to remote debugging switches (Chrome 136+)
  https://developer.chrome.com/blog/remote-debugging-port
- Chrome DevTools documentation, connecting to an existing browser session
  (Chrome 144+ opt-in and consent flow)
  https://developer.chrome.com/docs/devtools/agents/get-started/configuration
- Chrome DevTools for agents, auto-connect use case and security scope
  https://developer.chrome.com/docs/devtools/agents/use-cases/auto-connect
- Chrome DevTools MCP issue #825, request to persist approval (closed as not planned)
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/825
- Chrome DevTools MCP discussion #82, authenticated-page tradeoffs
  https://github.com/ChromeDevTools/chrome-devtools-mcp/discussions/82
- Developer discussion of a logged-in Chrome browser bridge and consent friction
  https://www.reddit.com/r/ClaudeAI/comments/1v5fz09/browser_bridge_an_mcp_server_that_drives_your/
- Reddit discussion of the Opus 5.5 reset: user-control preference and questions
  about scope and weekly reset timing
  https://www.reddit.com/r/ClaudeAI/comments/1wnecre/anthropic_is_offering_a_free_reset_to_explore/
- Anthropic Claude Code issue #95810, user report of `/limit-reset` returning
  “A session-limit reset isn't available right now.”
  https://github.com/anthropics/claude-code/issues/95810
- Reddit, report of `/limit-reset` behavior and its effect on the session limit
  https://www.reddit.com/r/ClaudeCode/comments/1w5r1hv/this_is_new_limitreset_resets_your_session_limit/
- Local read-only inspection of installed Claude Code 2.1.280 binary strings;
  `/limit-reset` was not executed.
