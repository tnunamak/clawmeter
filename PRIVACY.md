# Privacy Policy

Clawmeter is a local quota/status tool for AI coding services. It does not include analytics, advertising, crash reporting, telemetry, a Clawmeter account, or a Clawmeter-operated backend.

## Credentials

Clawmeter reuses credentials that the provider's own tools already store locally, or API keys that you explicitly configure. It does not ask for provider passwords.

| Provider | Credential source | Notes |
| --- | --- | --- |
| Claude | Claude Code's native credential source, plus any config directories you explicitly enroll | May refresh OAuth access. File-backed profiles write only the exact credential file they were read from; environment and macOS Keychain credentials are never written by Clawmeter. |
| Codex/OpenAI | Codex CLI and `auth.json` in the native or explicitly enrolled Codex home | Clawmeter prefers the local Codex app-server and may read the selected `auth.json` for read-only usage/reset-credit fallbacks. It never writes Codex credentials. |
| Antigravity | `~/.gemini/antigravity-cli/antigravity-oauth-token` | When the access token expires, Clawmeter reads the refresh token, discovers the OAuth client from the installed `agy` binary, and requests a new access token from Google. The refreshed access token stays in memory; Clawmeter does not rewrite the login file. A new Clawmeter process may refresh again after the usage cache expires. |
| Gemini | `~/.gemini/oauth_creds.json` and Gemini settings | May refresh an access token for API requests. |
| GitHub Copilot | `COPILOT_API_TOKEN` | Reads the token from the environment when configured. |
| Kimi | Kimi config, `KIMI_ACCESS_TOKEN`, or `KIMI_K2_API_KEY` | OAuth mode may refresh access tokens in memory only; Clawmeter does not rewrite Kimi's credential file. |
| OpenRouter | `OPENROUTER_API_KEY` or config | API-key based. |
| DeepSeek | `DEEPSEEK_API_KEY` or config | API-key based, read-only. Clawmeter calls only DeepSeek's documented `GET /user/balance` endpoint and reads the returned account balance; it never spends, tops up, or rotates the key. |
| Alibaba | Model Studio console sessions, `ALIBABA_CODING_PLAN_API_KEY`, `BAILIAN_CODING_PLAN_API_KEY`, or explicitly enrolled sources | Coding Plan and Personal Token Plan stay separate. Generic DashScope keys are not sent to Coding Plan quota endpoints. |

Clawmeter does not send provider credentials to Tim Nunamaker, GitHub, SignPath, or any Clawmeter service.

## Network Requests

Clawmeter contacts provider-owned APIs only to fetch quota, usage, account, or rate-limit status for enabled providers. These requests go to the provider you configured. For Antigravity, Clawmeter may refresh an expired access token through `oauth2.googleapis.com`, then calls Google's read-only `loadCodeAssist` and `retrieveUserQuotaSummary` methods. It does not submit prompts or consume model quota.

Clawmeter also checks GitHub Releases for application updates. The tray performs periodic update checks, and users can trigger update checks manually from the app. These requests go to GitHub's public API for `tnunamak/clawmeter` release metadata and do not include provider credentials.

The Windows installer exposes an automatic update-check option. Automatic update checks can also be disabled later in local config:

```bash
clawmeter config set check_for_updates false
```

Release downloads, checksums, and installer verification are served by GitHub Releases.

## Local Files

Clawmeter stores its own configuration and cache locally:

- Config: OS user config directory, for example `~/.config/clawmeter/config.yaml` on Linux.
- Cache: OS user cache directory, for example `~/.cache/clawmeter/usage.json` on Linux.

The usage cache stores derived quota/status data, recent provider errors, and opaque source-revision fingerprints so the tray and CLI avoid excessive polling and never reuse one account's data for another. For environment-backed sources, that fingerprint is a one-way SHA-256 hash of the selected route and high-entropy API credential. Raw credentials, environment-variable names, credential paths, account IDs, and email addresses are not stored in the cache. The cache file is private to the OS user (`0600` on Unix), and the default cache TTL is 60 seconds.

API credit pool snapshots are also stored in the user cache directory as `claude-api-credits-<first 16 hex characters of the pool hash>.json`. They include organization name, currency, balance, credit grants and expiry, month and daily spend, observation time, plan name, and monthly credit. The pool identity is an unsalted SHA-256 hash of `clawmeter-api-pool` plus the organization ID; a plan link uses a salted hash. Neither stores the raw organization ID or browser cookies.

Claude reset grant snapshots are stored the same way as `claude-web-resets*.json`. They hold reset expiry times, the observation time, the Claude source they belong to, and a salted hash of the organization ID.

## Browser Extension

The optional Clawmeter browser extension reads Claude data that your signed-in browser can already see and sends it only to Clawmeter on the same computer.

- **What it reads.** Once a minute, it reads your organizations and reset grants from claude.ai, and your organizations, prepaid credit balance, credit grants, and usage costs from the Claude Console (platform.claude.com). It reads claude.ai from inside each open claude.ai tab, because claude.ai refuses the extension's own requests. It reads the Console from inside each open Console tab and with the browser's main session. Tabs in different containers, where the browser has them, can be signed in to different accounts; ordinary tabs share one account.
- **Where it sends it.** Only to `http://127.0.0.1:17344`, the Clawmeter app on your computer. The extension makes no other network requests. Nothing goes to Tim Nunamaker, Anthropic beyond the normal page requests above, or any other party.
- **What it never touches.** It does not read or store cookies, passwords, chat content, prompts, or browsing history, and it does not change any page. It opens a tab only when you click **Open a pinned tab** in its popup. Your browser attaches its own Claude session to the requests, as it does when you open those pages yourself.
- **What it keeps.** The extension stores only the latest daily Console cost totals for each organization, keyed by a hash of the organization ID, in the browser's extension storage. It refreshes them every 30 minutes rather than every minute. They stay until the organization disappears from the Console, you sign out of the Console, or you remove the extension. It also stores whether it has ever read claude.ai, so that it can flag a claude.ai tab that has closed. For its toolbar popup it also keeps the result of the latest check (organization names, balances, reset counts and errors) in session storage, which is in memory only and is cleared when the browser closes. Clawmeter stores the derived snapshots described under Local Files.
- **Permissions.** `alarms` runs the once-a-minute check. `storage` holds the cost cache and the popup's latest check. `scripting` runs the reads inside open claude.ai and Console tabs. Host access to claude.ai and platform.claude.com is for the reads above, and to `127.0.0.1` for sending to Clawmeter.

Remove the extension from the browser's Extensions page to stop all of this. Clawmeter keeps showing the last saved readings, marked with their age.

Uninstalling Clawmeter removes installed binaries and shortcuts according to the installer. Local config and cache files may remain unless you delete them manually.

## How To Disable Providers

Disable a provider:

```bash
clawmeter config disable openai
clawmeter config disable claude
```

Inspect provider setup:

```bash
clawmeter providers
clawmeter doctor
```

## Third Parties

Clawmeter uses open-source dependencies listed in [Third-party components](docs/third-party-components.md).

Windows release signing is planned through SignPath Foundation if Clawmeter is accepted. Signing does not give SignPath access to user credentials or runtime quota data; SignPath receives release artifacts submitted by the GitHub Actions release workflow.
