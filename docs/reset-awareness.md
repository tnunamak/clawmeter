# Reset Awareness

Status: final PRD, revised 2026-09-24 to add Claude Web reset observations to
the shared reset-credit model and presentation.

## Research Note

Codex reset credits are fetched through a read-only endpoint. Claude's
promotional reset grants are visible to the signed-in browser's usage page but
not reliably to Claude Code OAuth. Clawmeter reads that page only when the user
starts a check and clicks their saved bookmark. The user starts the check from
one named Claude source, and Clawmeter stores the observation under that source.
It cannot verify that the browser and local source are the same account, so the
bookmark asks for confirmation before sending the result.

OpenAI's Codex docs say referral-earned rate-limit resets are banked and usable
for 30 days after grant, and the Codex changelog says `/usage` can show and
redeem earned usage-limit reset credits:

- https://developers.openai.com/codex/pricing
- https://developers.openai.com/codex/changelog
- https://help.openai.com/en/articles/6825453-chatgpt-release-notes
- https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan

Community pain points cluster around expiry visibility and redemption anxiety,
not around wanting more alerts. r/codex users report seeing available resets
without exact expiration dates, uncertainty about whether resets apply to 5h or
weekly usage, and concern that experimenting with `/usage` might accidentally
burn a reset:

- https://www.reddit.com/r/codex/comments/1ucdnhb/usage_resets_with_30_day_expiration_how_to_know/
- https://www.reddit.com/r/codex/comments/1uk8bim/how_to_see_when_usage_resets_expire/
- https://www.reddit.com/r/codex/comments/1u42cth/does_the_codex_referral_banked_reset_reset_weekly/
- https://www.reddit.com/r/codex/comments/1uiep6s/3_usage_limit_resets/

Other provider coverage checked:

- Claude and Claude Code expose normal usage windows and optional usage credits,
  but promotional `Reset for free` grants are read through a separate browser
  surface, not Claude Code OAuth:
  https://support.claude.com/en/articles/12429409-manage-usage-credits-for-paid-claude-plans
  See `ai/research/claude-reset-surface-2026-09-23.md` for the evidence and
  limits of the browser handoff.
- Gemini CLI / Gemini Code Assist document ordinary quota reset behavior, not
  banked reset credits:
  https://developers.google.com/gemini-code-assist/resources/quotas
- GitHub Copilot documents a monthly AI-credit allowance and reset cycle. The
  current account-quota surface reports runtime categories such as premium
  interactions, chat, and completions; it is not a banked reset-credit surface:
  https://docs.github.com/en/copilot/concepts/billing/usage-based-billing-for-individuals
  https://docs.github.com/en/copilot/how-tos/copilot-sdk/features/usage-and-billing
- OpenRouter exposes purchased-credit balances, not banked reset credits:
  https://openrouter.ai/docs/api/api-reference/credits/get-credits
  Clawmeter keeps that wallet balance separate from optional finite API-key limits;
  symbolic daily/weekly/monthly limit policies do not become guessed timestamps.
- Cursor, Kimi, and JetBrains AI expose quota or credit concepts, but no verified
  local read-only banked reset-credit surface currently supported by Clawmeter.

## Product Decision

The SLVP uses provider-specific retrieval with one shared inventory model and
one shared presentation:

1. Fetch Codex reset-credit metadata from the read-only endpoint only:
   `GET https://chatgpt.com/backend-api/wham/rate-limit-reset-credits`.
2. Never call any endpoint path containing `/consume`.
3. Normalize available credits and their individual expiries into
   `UsageResetCredits`. Show them under their own provider/account source in
   terminal output, `--agent`, JSON, and the tray provider menu / tooltip.
4. For Claude browser observations, persist expiry timestamps, check time,
   the matched local Claude source key, a random salt, and
   `sha256("clawmeter-claude-reset\0" + salt + "\0" + orgUuid)`. On read,
   drop the snapshot unless the source's current organization
   (`oauthAccount.organizationUuid` in its `.claude.json`) hashes to the same
   value. Never persist organization IDs or account/session details.
5. Avoid any visual or notification noise when no reset credits exist.
6. Fail soft if auth is missing, the endpoint changes, the network is down, or
   the provider rejects the request.

For Claude, the tray has one action, **Check Claude resets**. It starts a
10-minute loopback session and opens its page, `http://127.0.0.1:17343/check`.
The page always shows the same three steps: save the bookmark if you don't
have it, **Open Claude Usage**, click the bookmark there. A browser cannot tell
Clawmeter whether the bookmark still exists, so Clawmeter keeps no "set up"
state that could go stale. If the bookmark has not answered 45 seconds after
**Open Claude Usage**, the page says so and points back to step 1.
The page polls the session and shows the outcome: waiting, a retryable problem
(for example, an account that is not in Clawmeter), the result, or a timeout.
A timeout raises no tray notification.

The bookmark proves its account with
`sha256("clawmeter-claude-reset\0" + nonce + "\0" + orgUuid)`. Clawmeter computes
the same hash for every local Claude source and files the result under the one
that matches. No match, or two sources with the same organization, is rejected
with a short message and the session keeps waiting. The session accepts one
result, answers only `https://claude.ai` for the handoff, and serves only the
`127.0.0.1:17343` Host.

Which grants count. The bookmark sends each grant's remaining count
(`resets_left`), start (`starts_at`), expiry (`ends_at`), and `paused` flag.
Clawmeter counts a grant's remaining resets only while the grant is usable now:
started (`starts_at` not in the future), not paused, not expired, and with at
least one reset left. A paused or not-yet-started grant is not counted, even
though the Claude Usage page may list it. The evidence for these semantics is
thin: `ai/research/claude-reset-surface-2026-09-23.md` does not describe the
grant fields, and the field names come from the browser capture of the Usage
response only. The rule is therefore the conservative reading of the field
names. The bookmark and the local listener also refuse the whole result, and
send nothing, when a grant that has resets left lacks a boolean `paused`, a
parseable `starts_at`, or a parseable `ends_at`, because guessing would either
overcount or silently hide a grant. If Claude changes these fields, the
user sees "Claude's reset data changed format. Nothing was sent." instead of a wrong count.

Where it shows. A reset observation always appears under the Claude source it
was recorded for, in the tray, `clawmeter status` (plain, `--json`, `--agent`,
cached or fresh, and `clawmeter claude`), and the tooltip. This holds when
Claude usage is errored, expired, stale, rate limited, or unavailable; in the
last case the Claude row shows the observation without usage windows. The
browser observation is never a separate provider row, never appears in
`clawmeter providers`, diagnose, or icon cycling, and never attaches to a
different source key. If a Claude source ever reports its own non-empty reset
inventory, that provider-native inventory is shown and the browser observation
is dropped for display (Claude Code does not report one today).

Presentation. CLI and tray share one row: `Resets: 1 · expires Oct 22`, or
`Resets: 2 · next expires Oct 22` for several. Browser snapshots add
` · checked Oct 7`, or ` · checked 11:30` when checked today. Nothing shows
when no reset is available. JSON keeps the full fields; `--agent` prints
`observed_count=N snapshot=true` with exact timestamps.

Account identity. Native Default reads `~/.claude.json` (then
`~/.claude/.claude.json`); a config-dir source reads `<dir>/.claude.json`. A
source with no recorded organization cannot receive a result.

This slice intentionally does not add active notifications or generic windfall
detection. The research does not show that users need another alert channel; it
shows they need exact, low-anxiety inventory and expiry information. Retrieval
and authentication stay provider-specific; normalized reset metadata and
presentation are shared. Reset-event notifications remain a later feature once
we can throttle them against real state without producing false urgency.

The next slice keeps the same instrument-first stance and shows the existing
blocked-gap fact when a quota is projected to run out before its natural reset:

```text
runs out in 1d22h (1d8h before reset)
```

This is `RunsOutEarlyBy`: the projected wait between hitting 100% and the
natural reset. It is not a recommendation to redeem a reset. Showing it next to
reset-credit expiry gives users the missing subtraction without introducing
coach copy such as "use a reset now."

## UX Copy

Terminal plain/color output, when credits exist:

```text
7d: 66% (resets 3d6h, est. 124% at reset · runs out in 1d22h (1d8h before reset))
reset credits: 2 available, earliest expires Jul 12 2:30 PM
```

Agent output, when credits exist:

```text
runs_out_in=1d22h; runs_out_early_by=1d8h; reset_credits=[Codex available=2 earliest_expires_at=2026-07-12T14:30:00-05:00 earliest_expires_in=9d checked_at=2026-07-03T14:20:00-05:00]
```

Tray provider menu and tooltip, when credits exist:

```text
Runs out in 1d22h (1d8h before reset)
2 reset credits - earliest expires Jul 12 2:30 PM · checked 14:30
```

If the count exists but expiry metadata is missing:

```text
2 reset credits available
```

If no credits exist, show nothing. If the fetch fails, show nothing in primary
surfaces; diagnostic detail remains available through logs/tests only, without
raw responses or credentials.

## Non-goals

- Redeeming or consuming reset credits. Clawmeter must never do this.
- Recommendation or verdict copy such as "use a reset now." Clawmeter presents
  facts and calculated facts; users decide when to redeem credits.
- One generic provider retrieval mechanism. Each provider must use a
  separately verified read-only route; the shared model does not imply shared
  authentication or reset semantics.
- Push notifications for reset credits in this slice. Passive visibility solves
  the validated pain point without nagging.
- Inferring provider-wide global resets from noisy usage data. That needs
  persistence and false-positive controls before it belongs in the tray.
- Showing account IDs, emails, tokens, or raw provider responses.

## Edge Cases

- Missing, expired, wrong-account, or API-key-only Codex auth: omit reset-credit
  UI and keep normal usage behavior.
- 401, 403, 429, 5xx, offline, or timeout: fail soft and do not mark normal
  usage stale because reset credits are supplemental metadata.
- Endpoint shape changes: ignore malformed entries and omit the reset-credit UI
  if a safe summary cannot be produced.
- `available_count` disagrees with `credits[]`: display the first-party count
  but compute the earliest expiry from usable available, unconsumed, unexpired
  credit entries.
- Consumed, expired, unknown-status, or invalid-timestamp credits: do not use
  them for earliest-expiry guidance.
- Time zones and daylight savings: store provider timestamps as absolute times
  and display in local time.
- Multiple credits with the same expiry: sort deterministically and show the
  earliest expiry.
- Claude browser observations are snapshots. Keep a separate observation per
  local Claude source, show its full observation time, and never infer a match
  from browser organization identifiers.
- A browser observation can become outdated if the user redeems a grant in
  Claude before the next check. Show its checked time and never imply it is a
  live poll.
- Windows, macOS, and Linux tray differences: use existing menu/tooltip surfaces,
  not platform-specific notification behavior.

## Test Plan

- Unit-test parser filtering and ordering for available, consumed, expired,
  unknown-status, missing-field, and invalid-timestamp credits.
- Unit-test that the fetcher uses only the read-only reset-credit URL and never a
  path containing `/consume`.
- Unit-test required request headers with fake tokens and account IDs.
- Unit-test soft-fail behavior for missing auth and non-2xx responses.
- Unit-test provider cloning/cache compatibility for reset-credit metadata.
- Unit-test browser observation persistence, privacy allowlist, expiry filtering,
  per-source association, shared bookmark setup state, and stale presentation.
- Unit-test CLI and agent formatting.
- Unit-test tray tooltip/menu copy where possible without real tray APIs.
- Run `go test ./...` and `go test -tags tray ./...`.
- Smoke-test local `clawmeter --json` with output filtered to reset-credit fields
  only, so no credential or account detail is printed.

## Critical Review And Revision

Initial concept included reset/windfall notifications. That was too broad for the
evidence and would add noisy statefulness before the core value is proven. The
revised SLVP keeps the provider-specific read-only metadata path, surfaces the
validated reset-credit inventory and blocked-gap facts, and avoids action
prompts unless the user explicitly opens the existing quota surfaces. This is
smaller but stronger: it cannot waste a reset, cannot nag the user into burning
one, and degrades to the current Clawmeter experience on every unsupported
provider.
