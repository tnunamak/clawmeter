---
title: "Evidence audit: provider-quota-surfaces-2026-09-05"
date: 2026-09-05
topic: provider-usage
tags: [audit, quotas, evidence-review]
status: settled
---

## Scope

Reviews `ai/research/provider-quota-surfaces-2026-09-05.md` against the registered
provider roster in `internal/provider/all/all.go` and the adapter source under
`internal/provider/`. Read-only: no edits, no network calls, no provider endpoint
calls, no pricing/model-catalog research, no test/build gates run. Verification used
source reading and `git diff` against the working tree only.

## Roster coverage: complete, one-to-one

`all.go` registers exactly 15 providers: `alibaba`, `alibaba_token`, `antigravity`,
`kimi`, `kimik2`, `openai`, `gemini`, `copilot`, `deepseek`, `openrouter`, `jetbrains`,
`synthetic`, `xai`, `zai`, `claude`. The doc's Coverage Matrix has exactly 15 rows,
each mapping to one registered name under a display name (Codex→openai, Grok/xAI→xai,
Alibaba Coding Plan→alibaba, Alibaba Token Plan→alibaba_token). No provider is
missing, duplicated, or misassigned.

## Per-adapter claim verification

All 15 rows' specific technical claims (endpoints, field names, window/bucket
schemes) were checked against the corresponding adapter source and MATCH:

- Claude (`anthropic/`): `oauth/usage` endpoint; `five_hour`, `seven_day`,
  `seven_day_opus/sonnet/oauth_apps`, `weekly_scoped` model windows, `extra_usage`;
  local credential file/keychain discovery.
- Codex (`openai/`): app-server subprocess call; `direct_usage.go` reads
  `/backend-api/wham/usage`; `reset_credits.go` reads
  `/backend-api/wham/rate-limit-reset-credits`; local auth file.
- Gemini (`gemini/`): `retrieveUserQuota` RPC; Pro/Flash 24h bucket grouping;
  consumer-tier sunset error pointing to Antigravity migration.
- Antigravity (`antigravity/`): `retrieveUserQuotaSummary` RPC; weekly-window
  gate; Gemini/third-party pool grouping with safe fallback for unknown buckets.
- Copilot (`copilot/`): dual key-spelling support is real and precise — see below.
- Alibaba Coding Plan (`alibaba/`): `session_5h`/`weekly`/`monthly` windows;
  console-file and API-key-env credential kinds.
- Alibaba Token Plan (`alibabatoken/`): `.../v2/usage` and
  `.../v2/reset-card/list` operations; no consume call.
- Grok/xAI (`xai/`): separate Grok Build weekly pool fetch and management-key
  prepaid team balance fetch.
- OpenRouter (`openrouter/`): `/api/v1/key` spend cap and `/api/v1/credits`
  wallet balance, both optional.
- DeepSeek (`deepseek/`): `/user/balance`, per-currency balances only, no
  window/reset fields parsed.
- Kimi (`kimi/`): `coding/v1/usages` OAuth endpoint; flexible parsing, no fixed
  cadence assumed.
- Kimi K2 (`kimik2/`): third-party credits endpoint with a large alias list for
  consumed/remaining keys.
- JetBrains AI (`jetbrains/`): local `AIAssistantQuotaManager2.xml`; monthly
  credit/limit/refill fields.
- Synthetic (`synthetic/`): `v2/quotas` endpoint; rolling 5h, weekly (`7d`),
  search-hourly entries.
- z.ai (`zai/`): limits endpoint; token/time window names; epoch-millis reset.

**Copilot dual-key claim (the doc's headline finding): CONFIRMED, precisely.**
`copilot.go`'s `quotaSnapshot` struct carries both `percentRemaining` (legacy) and
`remainingPercentage`/`remaining_percentage` (current), plus per-snapshot
`resetDate`/`reset_date`. Snapshot lookup tries `["premium_interactions",
"premiumInteractions"]`, `"chat"`, `"completions"` in that order via
`findSnapshot`/`appendSnapshotWindow`. This matches the doc's "Changed and fixed"
row and its "Current Finding" narrative exactly.

## Correction needed: an undisclosed second behavior change

The working tree has a second real code change the document does not mention as a
change. `internal/diagnose/diagnose.go`'s `safeWindowNames` allowlist previously
had fixed scoped-model entries (`"7d Opus"`, `"7d Sonnet"`, `"7d Fable"`). The
working diff removes those fixed entries and replaces them with a generalized
`isSafeWindowName` / `isSafeModelSuffix` prefix-match scheme (`"7d "` + a bounded
ASCII suffix, max 24 chars, letters/digits/space/dot/hyphen only) tested against
both safe and unsafe (email-like, SQL-injection-like, over-length) inputs in
`diagnose_test.go`. `anthropic_test.go` was updated in the same diff to expect
`"7d Fable 5.1"` instead of `"7d Fable"`, confirming this is a live behavior
change tied to a model-name update, not a pre-existing property.

The research doc's Claude row says only "Unchanged; scoped model names remain
dynamic and bounded" — worded as a static property being reconfirmed, when the
mechanism that makes it true was itself modified in this same diff. `README.md`
and `docs/machine-interface.md` were updated to describe the new `7d <model>`
bounded-shape acceptance (so it is not undocumented in the repo overall), but the
audit document under review should either (a) name this as a second change
alongside the Copilot fix in "Current Finding," or (b) if intentionally scoped
out, say so explicitly, since its own Scope section claims to audit "model" and
"display" assumptions for exactly the file it touched.

This is a wording/completeness gap, not a factual error: every specific claim the
doc makes about Claude's behavior is accurate. The gap is an omission — the doc
implies the Copilot fix is the only change in this pass when a second file
(`diagnose.go`) also changed observable behavior (which scoped-model window names
survive into diagnostics output).

## Evidence classification of each doc claim

| Claim class | Classification | Basis |
|---|---|---|
| Copilot dual-key acceptance | Repository fixture/static contract (verified against live source) | Direct code read; matches doc |
| All other 14 adapters' endpoint/field/window claims | Repository fixture/static contract | Direct code read; matches doc |
| "Official Copilot billing and SDK quota docs" | Primary official documentation (URL cited, not re-fetched this pass — network calls out of scope) | Doc cites specific GitHub docs URLs; not independently re-verified per audit constraints |
| "current account observation exists" for Antigravity/Codex/Claude | Safe live observation (per doc's own "Unknowns" section) | Doc explicitly scopes this as local-observation-only, not fixture |
| Kimi/Kimi K2/JetBrains/z.ai "no stable public schema" / "no official schema" | Not evidenced (explicitly, by the doc's own admission) | Doc itself labels these experimental/heuristic; consistent with code comments using defensive aliasing |
| Generic plan-price/model-catalog claims (Copilot AI credits, Alibaba plan tables, xAI prepaid) | Correctly kept separate from provider-reported account data | Verified: no adapter merges plan-table constants into fetched quota values (confirmed by per-adapter code read) |

No wording was found that overstates an "unchanged" claim as freshly re-verified
live evidence when it was actually static-code-only, **except** the Claude row
described above, where "unchanged" glosses over an in-flight change to the
diagnostics safety filter that governs what that claim's evidence looks like at
the output boundary.

No wording confuses generic plan policy with provider-reported account data —
every row that touches a plan/pricing concept (Copilot AI credits, Alibaba
Coding/Token Plan documentation, xAI prepaid billing, OpenRouter wallet vs.
spend-cap) explicitly distinguishes the documented plan concept from the
adapter's actual read of account-specific values.

## Unavoidable evidence gaps (confirmed, not new)

These gaps are structural, not audit failures — they cannot be closed without
network calls or live multi-account testing that were out of scope for both this
review and the original doc:

- Kimi, Kimi K2, JetBrains, z.ai, and Antigravity's exact response schemas are
  not publicly documented; only in-repo fixtures/comments and prior observation
  back these adapters. This matches the doc's own "Unknowns" section.
- Whether GitHub's internal Copilot quota endpoint's new/legacy key coexistence
  is officially guaranteed (vs. a compatibility inference from adjacent SDK docs)
  cannot be settled without re-fetching GitHub's docs, which is out of scope here.
- Whether other accounts/regions/plans exercise code paths never observed
  locally (e.g., Alibaba console shapes on unobserved plan tiers) remains
  unverifiable without live testing, which this audit and the original excluded.

## Bottom line

The document is materially accurate: 15/15 provider rows map one-to-one to the
registered roster, and every specific adapter claim checked against source
matches. One completeness correction is needed: the Claude row's "Unchanged"
framing should acknowledge that `internal/diagnose/diagnose.go`'s scoped-window
safety filter changed in this same diff (fixed allowlist → bounded prefix match),
which is what makes "scoped model names remain dynamic and bounded" true going
forward, rather than presenting it as an already-settled fact re-confirmed by
this pass.
