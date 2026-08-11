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
| Kimi | Kimi config, `KIMI_ACCESS_TOKEN`, or `KIMI_K2_API_KEY` | OAuth mode may refresh access and write the provider's normal credential file. |
| OpenRouter | `OPENROUTER_API_KEY` or config | API-key based. |
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
