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

```powershell
powershell -ExecutionPolicy Bypass -NoProfile -Command "iwr https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.ps1 -OutFile $env:TEMP\install-clawmeter.ps1; & $env:TEMP\install-clawmeter.ps1 -Start"
```

Or download `.deb`, `.rpm`, macOS, Linux, and Windows binaries from the [latest release](https://github.com/tnunamak/clawmeter/releases/latest).

## Why Use It

- See your riskiest quota without opening provider dashboards.
- Compare current usage with expected pace for the reset window.
- Cycle the tray icon between concrete provider/quota windows.
- Reuse existing credentials without rewriting or refreshing provider tokens.

## Read The Icon

The provider logo stays in the circular center chip. The text names the quota window: `5H`, `7D`, `7A`, `7S`, or `MO`.

The radial meter compares actual burn with expected pace. Gray shows the shared baseline; a solid green segment means you are under pace, and a solid red segment means you are over pace.

- Left-click the tray icon to cycle through active provider/quota windows.
- Double-click the tray icon to return to Auto.
- Auto picks the riskiest quota and is not part of the left-click cycle.
- Right-click for details, refresh, update, and launch-at-login.
- Use `Refresh Now` when you want an immediate update.

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
clawmeter --check        # monitoring exit code
clawmeter update         # self-update
clawmeter tray           # run the tray
```

## Providers

| Provider | Tracks |
|---|---|
| Claude | 5h, 7d, model-specific windows |
| OpenAI/Codex | 5h and weekly rate limits |
| Gemini | 24h Pro and Flash quotas |
| GitHub Copilot | Premium and chat interactions |
| Kimi | Daily and hourly limits |
| OpenRouter | Credit balance |
| JetBrains AI | Monthly credits |
| Kimi K2 | Credit balance |

Unavailable providers stay hidden by default. Use `clawmeter --all` to see everything Clawmeter checked.

## Details

<details>
<summary>Install options</summary>

Linux installer defaults to a user install: binary in `~/.local/bin`, app launcher entry, no tray start, no launch-at-login, and no system package install. Pass `--start` to launch the tray now, or `--autostart` to enable launch-at-login.

```bash
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --dry-run
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --start --autostart
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --uninstall
```

Windows supports `-Start`, `-Startup`, and `-Uninstall` on `install.ps1`.

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
| Kimi | `~/.kimi/credentials/kimi-code.json` |
| OpenRouter | `OPENROUTER_API_KEY` or config |
| JetBrains AI | IDE config files |
| Kimi K2 | `KIMI_K2_API_KEY` or config |

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

Clawmeter reads existing credentials from your AI coding tools and queries their usage APIs. Results are cached at `~/.cache/clawmeter/usage.json` for 60 seconds, so the CLI and tray do not hammer provider APIs.
