---
title: "Provider quota and usage surface audit"
date: 2026-09-05
topic: provider-usage
tags: [quotas, limits, resets, accounts, providers, compatibility]
status: settled
sources: [copilot-sdk-usage, copilot-billing, gemini-cli-quota, gemini-api-limits, alibaba-coding-plan, alibaba-token-plan, openrouter-credits, openrouter-key, xai-billing, deepseek-balance, synthetic-quotas]
source_session: codex
---

## Scope

Audit the provider adapters registered in `internal/provider/all/all.go` on
2026-09-05. The audit checks quota, usage, reset, account, credential, model,
mode, and display assumptions. It does not update the separate minnows model or
pricing data pack. Provider-reported account data is kept separate from prices,
generic plan claims, and model availability.

Roster result: 15 registrations in `all.go`, 15 matrix rows, one-to-one after
mapping user-facing aliases such as Codex to `openai` and Grok to `xai`.

"Verified unchanged" below means that the current adapter still matches the
available current source or observed response contract. It does not mean that
every plan, account, region, or client was live-tested.

Evidence labels are explicit: **P** means an official primary source was
checked; **F** means repository parser fixtures/static tests exist; **L** means
the configured local account was safely observed on 2026-09-05; **S** means
community or other secondary evidence; and **N** means no authoritative source
was located in this bounded audit. N is not proof that a provider has no
documentation, and F is not proof that a fixture still matches every current
account response.

## Current Finding

Two compatibility changes are present in this working tree:

1. GitHub's current Copilot SDK documentation says the account quota response
   uses runtime snapshot keys commonly named `premium_interactions`, `chat`,
   and `completions`, with `remainingPercentage` and a per-snapshot
   `resetDate`. Clawmeter previously recognized only the older
   `premiumInteractions` key, `percentRemaining`, and a top-level reset date.
   The adapter now accepts both spellings and uses per-snapshot reset metadata
   when available. Unknown snapshot keys remain ignored, so new provider
   categories cannot inject arbitrary labels into diagnostics.
2. Claude's provider-reported model-scoped windows can change as models are
   released. The diagnostics safety filter previously allowed only three fixed
   names; it now accepts a bounded, validated `7d <model>` shape. This changes
   what safe model-window names can appear in diagnostics, while the Claude
   usage parser itself remains provider-driven.

This is a compatibility change only. Clawmeter does not turn Copilot's plan
prices, AI-credit allowance, or model pricing into a quota limit.

## Coverage Matrix

| Provider (canonical code name) | Current adapter surface | Primary source status | Fixture/static status | Safe live status | Result and remaining gap |
|---|---|---|---|---|---|
| Claude (`claude`) | OAuth usage; 5-hour, weekly, scoped-model, extra-usage windows; local credentials | P: model/config docs; N: OAuth usage schema | F: parser, normalization, and diagnostic-safety tests | L: Claude windows observed | Safety changed to accept bounded new scoped names; usage parser is unchanged and still partly undocumented |
| Codex (`openai`) | app-server limits; read-only ChatGPT usage; banked reset credits; local auth | P: model/config/reset-credit docs; N: quota JSON schema | F: rate-limit and reset-credit tests | L: 7-day quota and reset credits observed | Unchanged; quota rows remain duration-based because the response does not identify a model |
| Gemini (`gemini`) | Gemini CLI OAuth quota RPC; Pro/Flash grouping; consumer-tier migration handling | P: CLI quota and API-limit docs; N: internal RPC schema | F: quota/deprecation tests | Not observed in this audit | Unchanged; API-key and Vertex project limits are not treated as CLI subscription quota |
| Antigravity (`antigravity`) | CLI OAuth; weekly quota-summary RPC; Gemini and third-party pools | P: Google migration/deprecation docs; N: quota-summary schema | F: bucket and auth tests | L: weekly pools observed | Unchanged; new buckets use safe fallback labels and non-weekly windows are not guessed |
| GitHub Copilot (`copilot`) | Internal account quota; premium, chat, completion snapshots; reset metadata | P: SDK quota and billing docs; N: internal endpoint schema | F: legacy/current-shape tests | Not observed; locally disabled | **Changed and fixed:** current and legacy key/field spellings are accepted |
| Alibaba Coding Plan (`alibaba`) | Read-only console quota; 5-hour, weekly, monthly windows; API-key/console auth | P: plan policy docs; N: console operation schema | F: quota and region tests | Not observed in this audit | Unchanged; limits come from the account response, not plan tables |
| Alibaba Token Plan (`alibaba_token`) | Read-only usage and reset-card-list operations; personal 5-hour/7-day windows | P: Token Plan policy docs; N: console operation schema | F: usage/reset-card and consume-guard tests | Not observed in this audit | Unchanged; no consume operation is called |
| Grok/xAI (`xai`) | Grok Build weekly pool; xAI management prepaid balance | P: xAI billing/management docs; N: Grok Build RPC schema | F: balance and protobuf tests | Not observed in this audit | Unchanged; subscription pool and API wallet remain separate |
| OpenRouter (`openrouter`) | API-key spend cap; optional management-key wallet balance | P: credits/key API docs | F: key/wallet tests | Not observed in this audit | Unchanged; wallet balance is not presented as reset quota |
| DeepSeek (`deepseek`) | Read-only `/user/balance`; per-currency balances; no utilization inference | P: balance API schema | F: balance/status tests | Not observed in this audit | Unchanged; balance is not converted into a fabricated percentage |
| Kimi (`kimi`) | Kimi Code OAuth usage; flexible usage/limits parsing | N: stable public quota schema not located in bounded search | F: usage/limits tests | Not observed in this audit | Unchanged but experimental; no fixed cadence is assumed |
| Kimi K2 (`kimik2`) | Third-party credit endpoint with defensive aliases | N: authoritative public quota schema not located | F: credit parsing tests | Not observed in this audit | Unchanged but experimental; contract remains unknown |
| JetBrains AI (`jetbrains`) | Local IDE quota XML; monthly used/limit/refill fields | N: official local-file schema not located | F: XML parsing tests | Not observed in this audit | Unchanged but experimental; local state is treated as an observation |
| Synthetic (`synthetic`) | Read-only quotas; rolling 5-hour, weekly, search-hourly entries | P: quota policy/endpoint docs; N: complete response schema not retrieved | F: known-slot/fallback parser tests | Not observed in this audit | Unchanged but experimental; provider response remains authoritative |
| z.ai (`zai`) | Read-only limits; token/time limits and provider reset timestamps | N: official response schema not located; S: compatible secondary evidence | F: limit/unit parsing tests | Not observed in this audit | Unchanged but experimental; unit mapping remains heuristic |

## Provider Facts

### Codex and Claude

The current Claude model documentation uses aliases that resolve to current
provider model versions. Clawmeter therefore does not maintain a release-coupled
Claude model list. Its normalized provider response may contain new scoped model
names, which are accepted only in a bounded `7d <model>` shape for safe machine
diagnostics. Codex's current model documentation lists model choices separately
from the quota response; the adapter correctly does not claim that a quota row
belongs to the selected CLI model.

### Copilot

The current public billing policy describes monthly AI credits, model/token-based
accounting, and a first-of-month UTC reset. The SDK quota reference describes the
account-level entitlement as runtime categories and explicitly says the key set
is not type-validated. The internal endpoint may continue to use the older
camelCase fields, so the adapter retains those aliases. It does not assume that
all users have the same allowance or that every account has every category.

### Gemini and Antigravity

Gemini CLI's documented quota depends on authentication and tier. The API-key
and Vertex rate-limit surfaces are project/model limits, not evidence of a
consumer subscription. Clawmeter keeps those concepts separate. Google's
consumer-tier migration to Antigravity is already handled by the existing
deprecation detector. Antigravity's quota RPC remains an internal surface, so
the adapter reports only fields in the account response and fails soft on new
or incomplete buckets.

### Alibaba, OpenRouter, xAI, and DeepSeek

Alibaba's current Coding Plan documentation confirms simultaneous rolling
5-hour, weekly, and monthly request-call caps, while Token Plan documentation
describes separate personal credit windows and reset-card concepts. The
adapters read account/console values and do not hardcode those generic caps.

OpenRouter's documented credits endpoint is a management-key wallet query;
the documented key endpoint is an optional per-key spend cap. xAI documents
prepaid team credits and a separate management API. DeepSeek documents account
balances, not a usage-window quota. These three distinctions are preserved in
the output model.

## Unknowns and Follow-up Triggers

- Claude usage, Codex quota, Antigravity, Alibaba console, Grok Build, Kimi,
  z.ai, and JetBrains local-state schemas are not stable public contracts.
  A provider-side response fixture or a safe live observation is required
  before changing their parsers.
- Copilot's endpoint is internal even though the adjacent SDK quota contract is
  public. A future client-version gate or endpoint removal may require a new
  adapter strategy; this audit does not justify increasing its maturity.
- Gemini API-key/Vertex project limits, OpenRouter workspace budgets, xAI team
  spend limits, and Alibaba model-specific RPM/TPM limits are intentionally not
  merged into subscription quota rows. They need separate provider surfaces if
  Clawmeter later chooses to expose them.
- No provider besides Codex was found to expose a safe, confirmed banked reset
  credit inventory in the current supported adapters. Wallets, monthly credits,
  and ordinary quota resets are not reset credits.
- Live account coverage for this audit was limited to the configured local
  sources. The current observation included Antigravity, Codex, and Claude;
  other providers remain fixture- or documentation-validated unless a user has
  configured them.

## Sources

All web sources below were accessed on 2026-09-05.

- GitHub Copilot SDK usage and quota: https://docs.github.com/en/copilot/how-tos/copilot-sdk/features/usage-and-billing
- GitHub Copilot AI-credit billing: https://docs.github.com/en/copilot/concepts/billing/usage-based-billing-for-individuals
- Gemini CLI quotas: https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/quota-and-pricing.md
- Gemini API limits: https://ai.google.dev/gemini-api/docs/rate-limits
- Gemini consumer-tier deprecation and Antigravity migration: https://developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals
- Alibaba Coding Plan: https://www.alibabacloud.com/help/en/model-studio/coding-plan
- Alibaba Token Plan FAQ: https://docs.modelstudio.console.alibabacloud.com/en/model-studio/token-plan-personal-faq
- OpenRouter credits API: https://openrouter.ai/docs/api/api-reference/credits/get-remaining-credits
- OpenRouter current-key API: https://openrouter.ai/docs/api/api-reference/api-keys/get-current-key
- xAI billing: https://docs.x.ai/console/billing
- xAI management API: https://docs.x.ai/developers/rest-api-reference/management/billing
- DeepSeek balance API: https://api-docs.deepseek.com/api/get-user-balance/
- Synthetic quotas: https://dev.synthetic.new/docs/synthetic/quotas
- Claude model configuration: https://code.claude.com/docs/en/model-config
- Claude models overview: https://platform.claude.com/docs/en/models/overview
- Codex models: https://developers.openai.com/codex/models
- Codex configuration: https://developers.openai.com/codex/config-reference
- Codex pricing and reset-credit documentation: https://developers.openai.com/codex/pricing
- Codex changelog: https://developers.openai.com/codex/changelog
