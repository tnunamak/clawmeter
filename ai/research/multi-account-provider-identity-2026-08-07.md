# Multi-account and multi-source provider identity

Date: 2026-08-07

## Question

How should Clawmeter represent and monitor more than one independently quota-scoped
credential or subscription for a provider, starting with Claude Code, without
silently reporting the wrong quota or building a generic account-management system?

## Evidence

- Claude Code's official directory documentation says `CLAUDE_CONFIG_DIR` relocates
  every `~/.claude` path under the selected directory. This is the native mechanism
  for isolated Claude profiles: https://code.claude.com/docs/en/claude-directory
- Clawmeter currently keys its registry and cache by provider name, and the Claude
  adapter reads and writes only `~/.claude/.credentials.json`. That makes a second
  Claude profile invisible and leaves the write path tied to the default profile.
- CodexBar supports multiple quota sources. Its CLI exposes `--account`,
  `--account-index`, and `--all-accounts`; its config has named `tokenAccounts`;
  Codex accounts can come from multiple managed homes; and its Claude provider can
  optionally read `claude-swap` accounts. It keeps source labels in output and treats
  source selection as distinct from provider selection:
  https://github.com/steipete/CodexBar/blob/main/docs/cli.md
  https://github.com/steipete/CodexBar/blob/main/docs/configuration.md
- CodexBar's provider guide makes the useful boundary explicit: provider descriptors
  own identity and fetch strategy metadata, while account behavior belongs to the
  provider source rather than being guessed by the UI:
  https://github.com/steipete/CodexBar/blob/main/docs/provider.md
- CodexBar release notes mention profile-scoped Claude credential caching and keeping
  Team and Personal usage separate even when the email is the same. This supports
  stable account/profile identity instead of deduplicating by email:
  https://github.com/steipete/CodexBar/releases
- `claude-swap` uses named/aliased accounts, shows each account's 5-hour and 7-day
  usage, supports parallel sessions, and uses adaptive polling/backoff after 429s:
  https://github.com/realiti4/claude-swap
- `caam` separates vault profiles (switching), isolated profiles (parallel sessions),
  and shallow profiles (shared home with isolated auth files). It detects the active
  profile by hashing known auth files rather than trusting a mutable side channel:
  https://github.com/Dicklesworthstone/coding_agent_account_manager
- Other usage tools use explicit path overrides and, in some cases, a list of paths.
  `ccusage` documents `CLAUDE_CONFIG_DIR`; CodeBurn documents a path-list
  `CLAUDE_CONFIG_DIRS`. These are useful precedents for data-source selection, but
  they do not by themselves solve tray identity and quota presentation.

## Generalized conclusions

1. The primary domain object is a **usage source**, not an account. A source is one
   independently quota-scoped subscription, API key/project, organization/workspace,
   browser session, CLI profile, or other provider-defined credential scope.
2. The stable key must be `(provider family, source id)`, never just `provider` and
   never an email alone. Two sources may legitimately have the same email, and one
   source may have no human identity at all.
3. Credential discovery and source enrollment are different operations. A detected
   environment variable or local credential can be reported as discovered without
   silently enrolling every possible key into background polling.
4. Each source fetch must carry its own label, credential reference, cache key, error,
   stale state, forecast, and credential write target. One source's 401/429 must not
   hide or replace another source's good data.
5. Clawmeter should monitor sources, not switch or rotate credentials. Switching is a
   separate class of tool and belongs to provider-native tooling or explicit account
   managers such as `claude-swap`.
6. Quotas should not be aggregated across sources by default. Sources may have
   different units, plans, billing owners, reset rules, or eligibility. “Worst source”
   and “best runway source” are rankings, not combined capacity.

## Recommended implementation boundary

Add a small shared source-instance seam now:

- `ProviderID`: stable family such as `claude`, `openai`, or `gemini`.
- `SourceID`: stable local id such as `personal`, `work`, or `api-project-a`.
- `SourceLabel`: user-facing label; never require an email or provider identity.
- `CredentialRef`: provider-owned reference to an env var, file/profile, browser
  session, OAuth record, or API-key slot; never the secret itself.
- `UsageData`: carries the source identity through fetch, cache, JSON, ranking, and UI.

Keep discovery and credential resolution provider-owned. Claude can initially support
`CLAUDE_CONFIG_DIR` plus explicitly enrolled profile paths. An API-key provider can
later support named environment-variable or config-key sources without changing the
cache/UI contract. Do not build a universal credential scanner or provider-agnostic
discovery framework until a second real provider requires it.

## Implemented capability matrix (2026-08-11)

Clawmeter now carries `(provider family, source id)` through registration, fetch,
cache, JSON, ranking, CLI, and tray presentation. Provider adapters own validation
and exact credential routing:

| Provider family | Explicit routes |
| --- | --- |
| Claude | config directory |
| Codex | Codex home |
| Gemini | config directory |
| Antigravity | token file |
| Copilot | named environment variable or `hosts.json` |
| Grok/xAI | Grok home |
| Kimi | named environment variable or credential file |
| Kimi K2, Synthetic | named environment variable |
| OpenRouter | named standard-key or management-key environment variable |
| Alibaba Coding Plan | console file or named Coding Plan key environment variable |
| Alibaba Token Plan | console file |
| z.ai | named global or China key environment variable |
| JetBrains | exact quota file observation |

JetBrains files and OpenRouter key surfaces are separate observations, not proof of
independent additive quota pools. Clawmeter presents every source separately and never
adds their capacity. The first named source preserves a detected native source but does
not invent an unavailable default source. Exact paths and environment variable names
are non-secret selectors; they are not emitted in machine output, and cache revisions
are opaque. Environment credentials are re-observed on a bounded snapshot instead of
being memoized for the tray's lifetime. Every usage fetch compares source provenance
before and after the request; one stable retry accommodates provider-managed OAuth
refresh, while continued credential churn discards the response rather than caching it
under another account.
