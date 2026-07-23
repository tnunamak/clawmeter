# Privacy Policy

Clawmeter is a local quota/status tool for AI coding services. It does not include analytics, advertising, crash reporting, telemetry, a Clawmeter account, or a Clawmeter-operated backend.

## Credentials

Clawmeter reuses credentials that the provider's own tools already store locally, or API keys that you explicitly configure. It does not ask for provider passwords.

| Provider | Credential source | Notes |
| --- | --- | --- |
| Claude | `~/.claude/.credentials.json` | May refresh OAuth access and write the provider's normal credential file. |
| Codex/OpenAI | Local Codex CLI integration | Clawmeter delegates rate-limit reads to the local Codex CLI instead of directly reading OpenAI credentials. |
| Antigravity | `~/.gemini/antigravity-cli/antigravity-oauth-token` | Clawmeter asks the official `agy` CLI to refresh an expired login, then reads its access token. It does not read or store the refresh token. |
| Gemini | `~/.gemini/oauth_creds.json` and Gemini settings | May refresh an access token for API requests. |
| GitHub Copilot | `COPILOT_API_TOKEN` | Reads the token from the environment when configured. |
| Kimi | Kimi config, `KIMI_ACCESS_TOKEN`, or `KIMI_K2_API_KEY` | OAuth mode may refresh access and write the provider's normal credential file. |
| OpenRouter | `OPENROUTER_API_KEY` or config | API-key based. |

Clawmeter does not send provider credentials to Tim Nunamaker, GitHub, SignPath, or any Clawmeter service.

## Network Requests

Clawmeter contacts provider-owned APIs only to fetch quota, usage, account, or rate-limit status for enabled providers. These requests go to the provider you configured. For Antigravity, Clawmeter calls Google's read-only `loadCodeAssist` and `retrieveUserQuotaSummary` methods; it does not submit prompts or consume model quota.

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

The usage cache stores derived quota/status data and recent provider errors so the tray and CLI avoid excessive polling. The default cache TTL is 60 seconds. The cache is not intended to store raw provider credentials.

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
