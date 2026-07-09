# Clawmeter

<p align="center">
  <img src="assets/readme/tray-openai-5h-32.png" alt="OpenAI 5H tray icon" width="32" height="32">
  <img src="assets/readme/tray-openai-7d-32.png" alt="OpenAI 7D tray icon" width="32" height="32">
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

## Why Use It

- See your riskiest quota without opening provider dashboards.
- Compare current usage with expected pace for the reset window.
- See Codex banked reset credits and their earliest known expiry when available.
- Cycle the tray icon between concrete provider/quota windows.
- Reuse existing credentials without rewriting or refreshing provider tokens.

## Read The Icon

The provider logo stays in the circular center chip. The text names the quota window: `5H`, `7D`, `7A`, `7S`, or `MO`.

The radial meter compares actual burn with expected pace. Gray shows the shared baseline; a solid green segment means you are under pace, and a solid red segment means you are over pace.

- Left-click the tray icon to cycle through active provider/quota windows.
- Double-click the tray icon to return to Auto.
- Auto picks the riskiest quota and is not part of the left-click cycle.
- Right-click for details, refresh, update, and launch-at-login.
- A small blue dot on the tray icon means an update is available.
- Use `Refresh Now` when you want an immediate quota/update check.

## CLI

```bash
clawmeter
```

```text
PROVIDER   WINDOW    USAGE                 PCT  resets IN      PACE
Claude     5h        ██░░░░░░░░░░░░░░░░░░  11%  resets 4h00m   est. 55% at reset
           7d All    ██████████████████░░  89%  resets 1d10h   est. 112% at reset · runs out in 16h
           7d Sonnet ░░░░░░░░░░░░░░░░░░░░   2%  resets 4d23h   est. 7% at reset
```

Useful commands:

```bash
clawmeter providers      # detected providers and auth status
clawmeter claude         # one provider
clawmeter --json         # machine-readable output
clawmeter statusline     # compact Claude/statusline segment
clawmeter status --agent # token-efficient all-quota summary for AI agents
clawmeter setup --all    # install mainstream local integrations
clawmeter doctor         # provider and integration readiness
clawmeter --check        # monitoring exit code
clawmeter update         # self-update
clawmeter tray           # run the tray in this session
```

## Providers

| Provider | Tracks |
|---|---|
| Claude | 5h, 7d, model-specific windows |
| OpenAI/Codex | 5h and weekly rate limits; banked reset-credit expiry when available |
| Gemini | 24h Pro and Flash quotas |
| GitHub Copilot | Premium and chat interactions |
| Grok/xAI | API prepaid credits |
| Kimi | Daily and hourly limits |
| OpenRouter | Credit balance |
| JetBrains AI | Monthly credits |
| Kimi K2 | Credit balance |

Unavailable providers stay hidden by default. Use `clawmeter --all` to see everything Clawmeter checked.

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
clawmeter config enable openai
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
| OpenAI/Codex | `~/.codex/auth.json` |
| Gemini | `~/.gemini/` OAuth credentials |
| GitHub Copilot | `~/.config/github-copilot/hosts.json` |
| Grok/xAI | `XAI_MANAGEMENT_API_KEY`; optional `XAI_TEAM_ID` |
| Kimi | `~/.kimi/credentials/kimi-code.json` |
| OpenRouter | `OPENROUTER_API_KEY` or config |
| JetBrains AI | IDE config files |
| Kimi K2 | `KIMI_K2_API_KEY` or config |

For Grok/xAI, use a Management API key from xAI Console settings, not the
model-serving `XAI_API_KEY`. Clawmeter only reads prepaid credit balance and
usage metadata; it does not create, rotate, delete, or top up keys.

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
