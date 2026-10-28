# Clawmeter

<p align="center">
  <img src="assets/readme/tray-openai-5h-32.png" alt="Codex 5H tray icon" width="32" height="32">
  <img src="assets/readme/tray-openai-7d-32.png" alt="Codex 7D tray icon" width="32" height="32">
  <img src="assets/readme/tray-claude-7a-32.png" alt="Claude 7A tray icon" width="32" height="32">
  <img src="assets/readme/tray-claude-7s-32.png" alt="Claude 7S tray icon" width="32" height="32">
</p>

Clawmeter is a system tray and CLI quota meter for AI coding tools.

It answers the questions that matter while you are working: am I on track, what is most likely to run out, and when does it reset?

## Install

macOS:

```bash
brew install tnunamak/clawmeter/clawmeter
brew services start clawmeter
```

Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --start
```

Windows:

Download `ClawmeterSetup.exe` from the [latest release](https://github.com/tnunamak/clawmeter/releases/latest), verify it with `SHA256SUMS.txt`, then run the installer. It creates a Start Menu tray shortcut, adds `clawmeter` to your user `PATH`, and includes an uninstall entry.

Windows artifacts are currently unsigned. The future signing path is documented in the [Code signing policy](docs/code-signing.md) and [SignPath readiness checklist](docs/signpath-readiness.md).

```powershell
clawmeter providers
```

WinGet will install that same setup exe once the package is accepted:

```powershell
winget install --id tnunamak.Clawmeter -e
```

Advanced/manual fallback: download `clawmeter-windows-amd64.exe` from the same release if you only want the portable binary.

```powershell
.\clawmeter-windows-amd64.exe providers
```

You can also download `.deb`, `.rpm`, macOS, Linux, and Windows binaries from the [latest release](https://github.com/tnunamak/clawmeter/releases/latest).

Then run:

```bash
clawmeter setup --all
clawmeter doctor
```

Setup installs the mainstream local surface Clawmeter can verify today: a Claude Code statusline. Every agent can also pull the same cheap quota summary with `clawmeter status --agent`.

## Verify a download

Releases from v0.40.0 on sign `SHA256SUMS.txt` with [minisign](https://jedisct1.github.io/minisign/), and the self-updater refuses unsigned updates. To check a download yourself, see [SECURITY.md](SECURITY.md#verify-a-download).

## Why Use It

- See your riskiest quota without opening provider dashboards.
- Compare current usage with expected pace for the reset window.
- See reset credits and their earliest expiry under the matching provider source.
- Pin the tray icon to one provider/quota window, or let it follow the most at-risk one.
- Reuse existing credentials. When required, Clawmeter refreshes short-lived
  access tokens against the provider's own OAuth service. Claude file-backed
  profiles save refreshed tokens to the exact Claude Code credential file they
  were read from; Kimi refreshes stay in memory. See [Privacy Policy](PRIVACY.md).

### Claude Web Reset Grants

Claude's Usage page can show promotional reset grants that Claude Code's local
usage data does not include. The Clawmeter Chrome extension (see Claude API
Credits below) reads them each minute from an open claude.ai tab. claude.ai
refuses requests that do not come from its own pages, so keep a claude.ai tab
open; the extension also tries without one, but claude.ai usually refuses that.

The extension sends each organization's ID and grants (remaining count, start,
expiry, and paused state) to Clawmeter on this computer. Clawmeter keeps only
the organization a Claude source is signed in to, files the grants under that
source, and stores a salted hash of the organization ID, never the ID itself.
Only grants usable now count. Clawmeter stores no cookies or raw responses. If
you later sign that source in to a different organization, the saved result is
dropped.

The result shows under its Claude source as `Resets: 1 · expires Oct 22`. A
reading more than five minutes old adds when it was read and
`open claude.ai signed in to this account`.

### Announced Codex Resets (opt-in)

Off by default. Turn it on with:

```bash
clawmeter config set codex_reset_announcements true
```

Clawmeter then reads the public feed at codex-resets.com, a third-party site
that records posts by OpenAI Codex staff about global usage resets. When a post
schedules a reset that bears on a Codex account (a window is out now, or will
run out before its own reset), that account shows one line:

`Reset announced for ~10:00 · codex-resets.com`

The time is approximate and the source is unofficial. Clawmeter cannot tell
whether the reset reached your account. The line opens the post when you click
it in the tray. It never changes usage numbers, projections, or exit codes, and
OpenAI decides which accounts get a reset. Check the account's Usage page to
confirm. Once the announced time has passed, the line adds `unconfirmed`. See
[Privacy Policy](PRIVACY.md) for the request this makes.

### Quota Available Again (opt-in)

Off by default. Turn it on with:

```bash
clawmeter config set notify_quota_available true
```

When the tray has seen an account run out and a later reading of the same
account, within two poll intervals, shows room in every window that was out,
it sends one desktop notification, for
example `Codex quota available again` with `Codex: 5h 0% used, 7d 4% used` and
the time of the reading. It comes from the account's own reading, never from an
announcement, and once per time the account ran out. The tray keeps this in
memory, so starting it, a long gap between readings (the computer slept) or a
different account on the same source sets a new baseline instead of
notifying. A window missing from a reading keeps waiting for it. Quota on the meter does not prove a request
will succeed.

### Claude API Credits

Prepaid Claude API credits (for example the monthly API credit a Max plan
grants) have no API for their balance or expiry, and Anthropic's Usage & Cost
Admin API is unavailable to individual accounts. Clawmeter reads them from the
Claude Console through the Clawmeter Chrome extension:

To set it up, choose **Connect your browser for Claude resets and API
credits…** in the tray menu, or run `clawmeter setup --browser-extension`, then
follow the [setup page](https://tnunamak.github.io/clawmeter/extension/). It
covers Chrome, Brave and Edge, and more than one Claude account. The tray
offers this until the extension connects, and keeps it under Settings → Set up
the browser extension… for another browser or profile. `clawmeter doctor` shows
when the extension last connected. To report a bug, choose Settings → Report a
problem…, which opens a GitHub issue that names your Clawmeter version and
platform.

The extension checks each minute. A reading is live for up to five minutes;
older readings keep their numbers with a browser reminder, and signing out
shows a sign-in reminder. Usage-cost reports used for the pace are cached for
30 minutes; readings that no longer reconcile with current spend are rejected.

When a Claude plan funds the organization (the Console links it to that
plan's claude.ai organization), the pool shows on the Claude source signed in
to that plan:

```
Claude · Default: 5h: 1% … · API credits: $199.88 left · spent $0.11 this month · on pace to spend $1.84 more before it expires Oct 24 19:00
```

Other Console organizations show as their own pools under **Claude API**.
Clawmeter stores the plan link as a salted hash of the plan's organization ID,
as for reset grants, and the pool moves back under Claude API if that source
signs in elsewhere.

The balance is what the Console's billing page shows: purchased plus
promotional credit. Spend this month is the Console's own total. The pace is the
average daily cost from the Console's cost report over up to 7 days (at least
one day), and the projection extends it to the next credit expiry, or to when
the balance runs out first. Expiry shows in local time; the Console shows UTC
dates. The extension refuses to send data when the Console's daily costs and
its month total disagree. `clawmeter status --agent` lists every pool under
`api_credits=[...]` with exact amounts so agents can budget API spend.

The extension sends a hash of the Console organization ID, never the ID. It
sends the linked plan organization ID only to the local receiver, which hashes
it before saving. Clawmeter stores no cookies or raw organization IDs.

## Read The Icon

The provider logo stays in the circular center chip. The text names the quota window: `5H`, `7D`, `7A`, `7S`, or `MO`.

The radial meter compares actual burn with expected pace. Gray shows the shared baseline; a solid green segment means you are under pace, and a solid red segment means you are over pace.

- Click the tray icon for details, refresh, and update. Click a provider's name to open its dashboard.
- By default the icon follows the quota projected to block you soonest. Click a quota row to keep the icon on that quota; a check marks it. Click the row again to go back.
- When a provider shows an error or needs sign-in, click `Hide` in its block to remove it from the menu, and `Settings` > `Show hidden providers` and pick it to bring it back.
- A small blue dot on the tray icon means an update is available.
- Use `Refresh Now` when you want an immediate quota/update check.

## CLI

```bash
clawmeter
```

```text
PROVIDER  WINDOW  USAGE        USED  AT RESET  RESETS IN  RUNS OUT IN
Claude    5h      █▒┃▒▒▒░░░░░   11%       55%         4h
          7d      ████████┃█▒   89%      112%     1d 10h      16h 34m
```

The status table and the tray menu show the same values. The bar runs from 0 to 100% of the limit: █ used, ▒ projected by the reset, ░ free. ┃ marks where an even pace would be by now, so fill past it means you are ahead of pace. AT RESET is the forecast use when the window resets; over 100% means it runs out first. In a terminal, usage ahead of pace, a forecast over the limit and the run-out time are red, matching the tray icon, and a gap short of the pace mark is green. Windows with nothing used and nothing forecast are hidden; `--json` still has them. Providers are listed alphabetically, as in the tray menu.

`forecast soon` means too little of the window has elapsed: forecasting needs
10% of the window, capped at one hour. `no forecast` means the reset time or
window length is unknown. `forecast stale` means the reading cannot support a
current forecast, or its reset has passed. After a failed poll, a reading no
older than two configured poll intervals keeps its forecast and run-out estimate.
The forecast uses the rate at the time of that reading; the run-out countdown
subtracts its age. If that estimated run-out has passed, run-out is unknown
until another reading confirms usage. The last-good-data message still shows
that the poll failed.

Useful commands:

```bash
clawmeter providers      # detected providers and auth status
clawmeter providers diagnose codex --pretty  # privacy-safe live diagnostic
clawmeter providers diagnose claude --raw     # raw provider response per source (redacted), to verify a reading
clawmeter providers source list               # enrolled source ids and labels
clawmeter providers source add <provider> <id> <kind> [ref] [--label <label>]
clawmeter providers source remove claude work # final removal also disables the provider
clawmeter claude         # one provider
clawmeter codex          # Codex quota
clawmeter grok           # Grok quota
clawmeter --json         # machine-readable output
clawmeter statusline     # compact Claude/statusline segment
```

Restart a running tray after changing sources to apply the change.

Source enrollment uses provider-declared kinds. Run `clawmeter providers source help
<provider>` to see the exact routes that provider supports. `native` takes no `ref`;
file and directory routes require absolute paths; environment routes take the variable
name, never its secret value. Unsupported kinds fail before configuration is saved.

When the currently detected native source exists, adding the first named source keeps
it as `Default`. Otherwise Clawmeter adds only the named source, avoiding a broken empty
row. Every named source uses only its enrolled credential route. Clawmeter does not scan
for additional accounts or combine quotas across sources.

```bash
clawmeter providers source help codex
clawmeter providers source add codex work codex-home ~/.codex-work --label Work
```

```bash
clawmeter status --agent # token-efficient all-quota summary for AI agents
clawmeter setup --all    # install mainstream local integrations
clawmeter doctor         # provider and integration readiness
clawmeter --check        # monitoring exit code
clawmeter update         # self-update
clawmeter tray           # run the tray in this session
```

### Show quotas in T3 Code

T3 Code can show quotas from a CLIProxyAPI hub (Settings → Add a CLIProxyAPI hub). `clawmeter hub` answers the read-only part of that API, so T3 shows the Claude and Codex accounts Clawmeter tracks. An account T3 is also signed in to shares one row with T3's own reading of it.

```bash
export CLAWMETER_HUB_KEY="$(openssl rand -hex 32)"
clawmeter hub                     # listens on http://127.0.0.1:8318
```

In T3, enter `http://127.0.0.1:8318` as the hub URL and the same key as the management key. The hub answers from the cache the tray keeps, so keep the tray running. It never forwards a request or returns a credential. To let T3 match an account with its own login, the hub gives T3 the account's email, and for Codex its ChatGPT workspace ID and plan. It reads the Codex ones from the ID token in `auth.json`. For Claude, it asks Anthropic's profile endpoint with that account's token. It keeps the answer in memory until the login changes, and it retries a failed lookup after two minutes. T3 reads usage with an account index that changes with each login, so a read for an account listed before you log in again is refused instead of answered with the new login's numbers. A reading older than two tray polls (10 minutes by default) shows as unavailable in T3 rather than as current; a younger one is served even when the latest poll failed, for example on a rate limit. The hub does not offer Codex reset credits, because redeeming one needs a credit ID that Clawmeter does not have; T3 reads them through its own Codex login. Plain HTTP is allowed only on a loopback address; to serve another computer, pass `--tls-cert` and `--tls-key`, or put the hub behind a TLS proxy. The hub reads the configuration when it starts, so restart it after adding a source. It lists only accounts whose readings it can match to one login. Codex accounts qualify while Codex keeps its login in `auth.json`, its default; one set to `cli_auth_credentials_store = "keyring"`, `"auto"` or `"ephemeral"` in `config.toml` is left out. Claude sources added with a config directory always qualify. The Default Claude source qualifies once you have a second Claude source, which pins Default to a configured `oauth_token` or `~/.claude`. It is left out on macOS without a configured token, because Claude Code's Keychain login is read first and cannot be fingerprinted without reading it.

## Providers

| Provider | Tracks |
|---|---|
| Claude | 5h, 7d, model-specific windows |
| Codex | 5h and weekly rate limits; banked reset-credit expiry when available |
| Antigravity | Provider-reported shared model pools and time windows from `agy /usage` |
| Gemini | 24h Pro-model and Flash-model quotas; Code Assist tier checked when available |
| GitHub Copilot | Provider-reported premium, chat, and completion quotas when available |
| Grok/xAI | Grok weekly usage pool; API prepaid credits |
| Kimi | Usage and rate limits; cadence is provider-reported/experimental |
| OpenRouter | Wallet credit balance and finite API-key limits |
| JetBrains AI | Monthly credits |
| Kimi K2 | Credit balance |
| Alibaba Coding Plan | Coding Plan 5-hour, weekly, and monthly quotas |
| Alibaba Token Plan | Personal Token Plan 5-hour, 7-day, and reset-credit data after one-time Model Studio quota connection |
| DeepSeek | Account balance only (read-only); no utilization, spend, or reset-time data |
| OpenCode Free | Local daily activity and observed free-limit events per zero-priced model; quota and reset remain unknown |

Unavailable providers stay hidden by default. Use `clawmeter --all` to see everything Clawmeter checked.

Model selection and quota windows are separate. Claude's model-scoped weekly windows use
the names returned by Anthropic, including new model names. Codex's quota response does
not identify the active model, so Codex rows stay labeled by the provider and the
reported time window rather than guessing from the CLI model selection.

Provider maturity is binary and intentionally kept out of quota rows and the tray. The `clawmeter providers` inventory and `--json` metadata identify experimental integrations and link to [provider maturity](docs/provider-maturity.md).

Automation should use the versioned [`--json` machine interface](docs/machine-interface.md), not parse terminal output. Its schemas, compatibility policy, diagnostic command, and consumer guidance are published with the repository.

Codex reset-credit visibility is read-only. Clawmeter shows available count and earliest expiry when Codex exposes banked resets locally, but it never redeems resets. Design notes and provider coverage are in [Reset awareness](docs/reset-awareness.md).

## Details

<details>
<summary>Agent integrations</summary>

```bash
clawmeter setup --all
clawmeter setup --claude-statusline
clawmeter setup --dry-run --all
clawmeter doctor
```

`clawmeter statusline` is cache-only, so Claude Code can call it frequently without refreshing provider APIs or burning quota. `clawmeter status --agent` is the high-precision pull command for Codex, Claude, Gemini CLI, or any other agent when quota context is useful.

tmux users can opt in explicitly:

```bash
clawmeter setup --tmux
```

</details>

<details>
<summary>Install options</summary>

Linux installer defaults to a user install: binary in `~/.local/bin`, app launcher entry, no tray start, no launch-at-login, and no system package install. Pass `--start` to launch the tray now, or `--autostart` to enable launch-at-login.

```bash
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --dry-run
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --start --autostart
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --uninstall
```

Windows users should prefer `ClawmeterSetup.exe`. WinGet is the package-manager path after acceptance. `clawmeter-windows-amd64.exe` is the portable fallback. `install.ps1` remains an advanced scripting fallback with `-LocalBinary`, `-Start`, `-Startup`, and `-Uninstall`; if you use it directly, download the script first and run it from a folder you trust. Avoid copy-pasting download-and-execute one-liners.

macOS Homebrew installs a local app wrapper. To show it in Applications/Launchpad:

```bash
mkdir -p ~/Applications
ln -sfn "$(brew --prefix clawmeter)/Clawmeter.app" ~/Applications/Clawmeter.app
```

</details>

<details>
<summary>Configuration</summary>

Config file: `~/.config/clawmeter/config.yaml`

```bash
clawmeter config show
clawmeter codex
clawmeter grok
clawmeter providers enable openrouter
clawmeter config disable copilot
clawmeter config set poll_interval 600
clawmeter config set warning_threshold 80
clawmeter config set critical_threshold 95
```

</details>

<details>
<summary>Launch at login</summary>

```bash
clawmeter tray --install
clawmeter tray --uninstall
```

`clawmeter tray` starts the tray for the current desktop session. For a persistent desktop tray, use the installer/Start Menu shortcut or `clawmeter tray --install`.

On macOS with Homebrew, you can also use:

```bash
brew services start clawmeter
brew services stop clawmeter
```

</details>

<details>
<summary>Provider credential sources</summary>

| Provider | Source |
|---|---|
| Claude | Claude Code OAuth token |
| Codex | `~/.codex/auth.json` |
| Antigravity | `~/.gemini/antigravity-cli/antigravity-oauth-token` from `agy` login |
| Gemini | `~/.gemini/` OAuth credentials |
| GitHub Copilot | `~/.config/github-copilot/hosts.json` |
| Grok/xAI | `~/.grok/auth.json` from `grok login`; or `XAI_MANAGEMENT_API_KEY` with optional `XAI_TEAM_ID` |
| Kimi | `~/.kimi/credentials/kimi-code.json` |
| OpenRouter | `OPENROUTER_API_KEY` or config; optional `OPENROUTER_MANAGEMENT_KEY` for wallet credits |
| JetBrains AI | IDE config files |
| Kimi K2 | `KIMI_K2_API_KEY` or config |
| Alibaba Coding Plan | Dedicated Model Studio console login (`bl auth login --console`) or `ALIBABA_CODING_PLAN_API_KEY` / `BAILIAN_CODING_PLAN_API_KEY` (Coding Plan key only) |
| Alibaba Token Plan | One-time Model Studio quota connection via `clawmeter providers connect token-plan`; existing `~/.bailian` session is reused |
| DeepSeek | `DEEPSEEK_API_KEY` or config |

For Grok/xAI, `grok login` enables Grok weekly usage-pool tracking from the
read-only grok.com billing surface. `XAI_MANAGEMENT_API_KEY` enables xAI API
prepaid-credit tracking from the Management API; use a Management API key from
xAI Console settings, not the model-serving `XAI_API_KEY`. Clawmeter does not
create, rotate, delete, top up, or spend anything.

For Alibaba Coding Plan, Clawmeter reads the official Model Studio CLI's
console-login session from its dedicated Coding Plan profile or `~/.bailian`.
It sends only the read-only quota query, never reads browser cookies and never
calls a model or plan-management endpoint. Personal Token Plan remains a
separate provider and session profile.

For Alibaba Token Plan, run `clawmeter providers connect token-plan` once to
authorize the official Model Studio console session. Clawmeter then reads only
the read-only quota and reset-card operations. `BAILIAN_TOKEN_PLAN_API_KEY`
continues to be useful for model requests, but it does not provide account-level
Token Plan quota and is never treated as quota authorization. If the official
Bailian CLI is missing, Clawmeter prints Alibaba's documented install command;
it does not silently download executable code.

DeepSeek is read-only balance semantics. Clawmeter calls only the documented
`GET /user/balance` endpoint and reports the account balance per currency; it
never exposes utilization percentage, spend, quota totals, or reset timing,
because DeepSeek's balance API does not provide them. Clawmeter never tops up,
spends, or rotates the key. OpenCode's temporary free DeepSeek route is a
separate inference service; this balance provider does not measure its
availability, usage fees, or limits.

</details>

<details>
<summary>Build from source</summary>

```bash
git clone https://github.com/tnunamak/clawmeter.git
cd clawmeter
make install          # CLI only
make install-tray     # with system tray support
```

Tray builds require CGO. Linux also needs `libayatana-appindicator3-dev`.

</details>

## How It Works

Clawmeter reads existing credentials from your AI coding tools and queries their usage APIs. Results are cached at `~/.cache/clawmeter/usage.json` for 60 seconds, so the CLI and tray do not hammer provider APIs. See [Privacy Policy](PRIVACY.md), [Security Policy](SECURITY.md), and [Third-party components](docs/third-party-components.md).
