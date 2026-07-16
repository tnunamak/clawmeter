# Reset Awareness

Status: final PRD for the first shipped slice, revised after product review on
2026-07-03. A second small shipped slice adds blocked-gap visibility next to
existing run-out estimates.

## Research Note

Codex is the only currently supported Clawmeter provider with banked usage-limit
reset credits that are locally discoverable without consuming anything. OpenAI's
Codex docs say referral-earned rate-limit resets are banked and usable for 30
days after grant, and the Codex changelog says `/usage` can show and redeem
earned usage-limit reset credits:

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
  but not banked reset credits in the same sense:
  https://support.claude.com/en/articles/12429409-manage-usage-credits-for-paid-claude-plans
- Gemini CLI / Gemini Code Assist document ordinary quota reset behavior, not
  banked reset credits:
  https://developers.google.com/gemini-code-assist/resources/quotas
- GitHub Copilot documents monthly premium-request allowances and reset cycles,
  with no rollover for unused requests:
  https://docs.github.com/en/copilot/concepts/billing/copilot-requests
- OpenRouter exposes purchased-credit balances, not banked reset credits:
  https://openrouter.ai/docs/api/api-reference/credits/get-credits
  Clawmeter keeps that wallet balance separate from optional finite API-key limits;
  symbolic daily/weekly/monthly limit policies do not become guessed timestamps.
- Cursor, Kimi, and JetBrains AI expose quota or credit concepts, but no verified
  local read-only banked reset-credit surface currently supported by Clawmeter.

## Product Decision

The first lovable slice is passive Codex reset-credit visibility:

1. Fetch Codex reset-credit metadata from the read-only endpoint only:
   `GET https://chatgpt.com/backend-api/wham/rate-limit-reset-credits`.
2. Never call any endpoint path containing `/consume`.
3. Show available reset count and earliest expiry where users already look:
   terminal output, `--agent`, JSON, and the tray provider menu / tooltip.
4. Avoid any visual or notification noise when no reset credits exist.
5. Fail soft if auth is missing, the endpoint changes, the network is down, or
   the provider rejects the request.

This slice intentionally does not add active notifications or generic windfall
detection. The research does not show that users need another alert channel; it
shows they need exact, low-anxiety inventory and expiry information. Reset-event
notifications remain a later feature once we can throttle them against real
state without producing false urgency.

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
runs_out_in=1d22h; runs_out_early_by=1d8h; reset_credits=[Codex available=2 earliest_expires_at=2026-07-12T14:30:00-05:00 earliest_expires_in=9d]
```

Tray provider menu and tooltip, when credits exist:

```text
Runs out in 1d22h (1d8h before reset)
2 reset credits - earliest expires Jul 12 2:30 PM
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
- A generic reset-credit framework for every provider. Current evidence only
  supports Codex.
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
