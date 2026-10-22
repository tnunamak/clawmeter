# Reset Awareness

Status: final PRD, revised 2026-09-24 to add Claude Web reset observations to
the shared reset-credit model and presentation.

## Research Note

Codex reset credits are fetched through a read-only endpoint. Claude's
promotional reset grants are visible to the signed-in browser's usage page but
not reliably to Claude Code OAuth. The Clawmeter browser extension reads the
same data each minute from an open claude.ai tab. Clawmeter files the
observation under the local Claude source signed in to the same organization,
matched by a salted hash, so there is nothing to confirm.

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

For Claude, the Clawmeter browser extension reads
`/api/organizations/{id}/usage` for each organization the browser is signed in
to, once a minute. claude.ai answers 403 to requests from the extension's
background worker, so the extension runs the request inside an open claude.ai
tab, in an isolated script world the page cannot change, and falls back to the
background request only when no tab is open. It posts each organization's ID
and grants to `http://127.0.0.1:17344/v1/resets`, which admits only the
extension's fixed origin. Clawmeter files the reading under the one local
Claude source signed in to that organization and drops readings for
organizations it does not track or that match two sources. A reading older
than the saved one, for example from a second browser profile, is dropped.

Which grants count. The extension sends each grant's remaining count
(`resets_left`), start (`starts_at`), expiry (`ends_at`), and `paused` flag.
Clawmeter counts a grant's remaining resets only while the grant is usable now:
started (`starts_at` not in the future), not paused, not expired, and with at
least one reset left. A paused or not-yet-started grant is not counted, even
though the Claude Usage page may list it. The evidence for these semantics is
thin: `ai/research/claude-reset-surface-2026-09-23.md` does not describe the
grant fields, and the field names come from the browser capture of the Usage
response only. The rule is therefore the conservative reading of the field
names. The extension and the local receiver also refuse the whole result, and
save nothing, when a grant that has resets left lacks a boolean `paused`, a
parseable `starts_at`, or a parseable `ends_at`, because guessing would either
overcount or silently hide a grant. If Claude changes these fields, the
saved reading ages instead of showing a wrong count.

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
`Resets: 2 · next expires Oct 22` for several. A browser reading more than
five minutes old adds ` · not updated since 11:30 · open claude.ai in your
browser` (a date instead of a time when it is older than today). Nothing shows
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
  local Claude source, keep its full observation time, and never infer a match
  from browser organization identifiers.
- A browser observation can become outdated if the extension stops reading
  claude.ai (browser closed, no claude.ai tab). After five minutes, show when it
  was read and how to refresh it.
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
  per-source association, and stale presentation.
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

## Codex Reset Announcements

Status: design, 2026-10-22. The owner asked whether the public feed at
https://codex-resets.com can add value. It records posts in which an OpenAI
Codex lead announces global Codex usage resets, and it classifies some posts
as scheduled ("lands tomorrow before 10am PST") or as AI forecasts.

### What the evidence supports

- The feed is third party and unofficial. OpenAI's help article on Codex
  resets makes eligibility and timing discretionary, says a global reset may
  not reach every account or every limit, and names the account's Usage page
  as the place to confirm one. A global reset also does not refund a banked
  reset already redeemed: https://help.openai.com/en/articles/20001498-how-resets-work-in-codex
- The feed cannot say when a reset happened. On 2026-10-22 its status still
  listed the 17:00 UTC reset as scheduled at 20:31 UTC, after the owner had
  received it. None of its 64 history events records an execution time; they
  carry only `announced_at`.
- It carries no account identity or eligibility. Clawmeter's own Codex reading
  is the only source for "can this account work now".
- Its forecasts have no published calibration history, so a percentage cannot
  be judged.

Research record: divan `ai-quota-monitoring/codex-reset-announcements-do-not-prove-account-quota-or-scheduled-execution.md`.

### The one decision it informs

A Codex account that is out, or will run out before its own reset, faces a
choice: wait, switch to another provider, or redeem a banked reset. A reset
announced for a time before the account's own reset changes that choice, and
nothing else in Clawmeter can tell the user about it. Clawmeter states the
announcement and leaves the decision to the user; it does not advise waiting
or holding a banked reset (the PRD's non-goal on recommendation copy stands).

### What Clawmeter shows

One line on each Codex account, only while it bears on that account. A window
qualifies when it is out now or is projected to run out before its own reset
(the existing "out now" and "runs out in" facts), and its own reset comes
after the announced time. The line shows while:

- the feed's current `scheduled_reset` is a `regular` reset with a
  `scheduled_for` time, which is no more than six hours in the past;
- the account's latest reading is fresh (not stale, errored or expired) and
  has a qualifying window;
- the feed was read successfully within the last two hours.

Copy: `Reset announced for ~10:00 · codex-resets.com` (a date as well when the
time is not today). The tilde marks the feed's time as approximate: posts say
things like "before 10am PST". The line carries no claim about whether the
reset reached the account; the window rows above it already say whether the
account is still out. It sits after the reset-credit row, the last line of the
account. In the tray it opens the announcement's source post.

Every surface shows the same fact: the tray row and tooltip, `clawmeter
status`, `--json` and `--agent`. JSON adds to the Codex source an
`announced_reset` object: `kind` ("third_party_announcement"), `official`
(false), `feed` ("codex-resets.com"), `source_url`, `announced_at`,
`scheduled_for`, `approximate` (true), `windows` (the qualifying window names),
`feed_checked_at`, and `shown_until` (`scheduled_for` plus six hours). The
agent line adds `announced_reset=[~<time> unofficial source=codex-resets.com
windows=7d]`. The announcement never changes quota values, projections,
natural reset times, risk status, icon choice or exit codes.

Not shown: AI forecasts and percentages, banked or mixed posts, the latest
executed reset, a feed list, notifications of any kind, and advice.

### Fetching

Off by default. The feed is a third party and unofficial, and Clawmeter
otherwise talks only to providers the user signed in to, so the user turns it
on with `codex_reset_announcements: true` in the config; it then runs only when
a Codex source is configured. Only `GET /api/v1/status` is requested, with no
account data. The response is cached in the Clawmeter cache directory with its
ETag and the time of the last successful read, shared by every process. A read
younger than 15 minutes is reused; an older one is revalidated with
`If-None-Match`, ignoring the response's own max-age. 429 and 503 honour
Retry-After, other failures back off to 15 minutes, and a failure keeps the
last good reading, which stops showing two hours after its last successful
read. Bodies are size-limited and validated; any unexpected shape shows
nothing. A changed ETag with the same `scheduled_reset` is not a new event.

### Edge cases

- Several Codex accounts: each decides from its own windows; one may show the
  line while another does not.
- A reset that refills one window and not another: the line stays while any
  window still qualifies, and names it in JSON and agent output.
- Time zones: times are absolute and shown in local time; machines get RFC 3339.
- A schedule moved or withdrawn by a later post: the feed's current
  `scheduled_reset` wins; nothing is remembered between readings.
- An account reading that is stale: no line; the row already says the reading
  is stale.
- Offline or feed down: the line stops two hours after the last successful
  read; never an error in the tray.
