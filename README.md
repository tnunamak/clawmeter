# Clawmeter

Monitor your AI coding assistant usage quotas from the terminal and system tray.

Tracks rate limits and usage across Claude, OpenAI/Codex, Gemini, GitHub Copilot, Kimi, OpenRouter, JetBrains AI, and more — so you can see where you stand without leaving the terminal.

## Install

### macOS (Homebrew)

```bash
brew install tnunamak/clawmeter/clawmeter
brew services start clawmeter   # start tray
```

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh
```

Downloads the binary to `~/.local/bin`, starts the system tray, and enables launch at login. Installs tray dependencies automatically if missing.

#### .deb / .rpm

Each [release](https://github.com/tnunamak/clawmeter/releases/latest) includes `.deb` and `.rpm` packages for amd64 and arm64:

```bash
# Debian/Ubuntu
sudo dpkg -i clawmeter_*.deb

# Fedora/RHEL
sudo rpm -i clawmeter-*.rpm
```

Installs to `/usr/bin/clawmeter`. Start the tray with `clawmeter tray`.

### Windows

Download `clawmeter-windows-amd64.exe` from the [latest release](https://github.com/tnunamak/clawmeter/releases/latest), rename to `clawmeter.exe`, and place it somewhere on your PATH.

To run the system tray:

```powershell
clawmeter tray
```

### Other methods

```bash
# Go install (CLI only, no tray)
go install github.com/tnunamak/clawmeter/cmd/clawmeter@latest

# Build from source
git clone https://github.com/tnunamak/clawmeter.git
cd clawmeter
make install          # CLI only, pure Go
make install-tray     # with system tray support (requires CGO)
```

Tray prerequisites (Linux): `sudo apt install libayatana-appindicator3-dev`

### Uninstall

```bash
# macOS
brew services stop clawmeter
brew uninstall clawmeter

# Linux
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh -s -- --uninstall
```

## Providers

Clawmeter auto-detects credentials for each provider. No configuration needed for most setups.

| Provider | Source | What it tracks |
|----------|--------|----------------|
| Claude | Claude Code OAuth token | 5h, 7d, model-specific windows |
| OpenAI/Codex | `~/.codex/auth.json` (API key or ChatGPT OAuth) | 5h and weekly rate limits |
| Gemini | `~/.gemini/` OAuth credentials | 24h Pro and Flash quotas |
| GitHub Copilot | `~/.config/github-copilot/hosts.json` | Premium and chat interactions |
| Kimi | `~/.kimi/credentials/kimi-code.json` | Daily and hourly limits |
| OpenRouter | `OPENROUTER_API_KEY` env var or config | Credit balance |
| JetBrains AI | IDE config files | Monthly credits |
| Kimi K2 | `KIMI_K2_API_KEY` env var or config | Credit balance |

List all providers and their status: `clawmeter providers`

## CLI

```
$ clawmeter
PROVIDER   WINDOW    USAGE                 PCT  resets IN      PACE
Claude     5h        ██░░░░░░░░░░░░░░░░░░  11%  resets 4h00m   ✓ 9% ahead    · lasts to reset
           7d All    ██████████████████░░  88%  resets 1d10h   ⚠ 8% behind   · runs out in 18h
           7d Sonnet ░░░░░░░░░░░░░░░░░░░░   2%  resets 4d23h   ✓ 27% ahead   · lasts to reset

Gemini     24h Pro   ░░░░░░░░░░░░░░░░░░░░   0%  resets 23h59m  ✓ on pace     · lasts to reset
           24h Flash ░░░░░░░░░░░░░░░░░░░░   0%  resets 23h59m  ✓ on pace     · lasts to reset

OpenRouter credits   ░░░░░░░░░░░░░░░░░░░░   0%  resets 12mo4d  ✓ on pace     · lasts to reset
```

- Color-coded bars and indicators: green (on pace), yellow (tight), red (projected to exceed)
- Providers sorted by urgency — most critical first
- Unavailable providers hidden by default (use `--all` to show)

### Commands

```
clawmeter                          # show all providers (default)
clawmeter claude                   # show a specific provider
clawmeter --json                   # full JSON with forecasts
clawmeter --plain                  # no color (also auto-detected when piped)
clawmeter --check                  # exit code for monitoring scripts
clawmeter --all                    # include unavailable providers
clawmeter providers                # list all providers and their status
clawmeter config show              # show configuration
clawmeter config enable openai     # enable a provider
clawmeter config disable copilot   # disable a provider
clawmeter update                   # self-update to latest release
clawmeter version                  # show version
```

### Exit codes (`--check`)

| Code | Meaning |
|------|---------|
| 0 | All providers healthy |
| 1 | At least one provider at warning level (projected 90%+) |
| 2 | Critical (projected 100%+), expired, or error |

## System tray

The installer starts the tray automatically. To launch manually: `clawmeter tray`

- Color-coded icon: green (on pace), yellow (tight), red (projected to exceed), gray (expired)
- Hover tooltip shows usage for all providers
- Polls every 5 minutes with "Refresh Now" for immediate update
- Desktop notifications at 80% and 95% thresholds
- "Launch at login" toggle in the menu

```bash
clawmeter tray --install    # enable launch at login
clawmeter tray --uninstall  # disable launch at login
```

## Status page integration

Clawmeter checks operational status for providers that have public status pages (Claude, OpenAI, GitHub Copilot, OpenRouter). Only components relevant to coding tools are monitored — e.g., "Codex" and "Chat Completions" for OpenAI, not "Sora" or "DALL-E".

## Configuration

Config file: `~/.config/clawmeter/config.yaml`

```bash
clawmeter config show                          # show current config
clawmeter config set poll_interval 600         # tray poll interval (seconds)
clawmeter config set warning_threshold 80      # notification threshold (%)
clawmeter config set critical_threshold 95     # notification threshold (%)
```

## How it works

Clawmeter reads existing credentials from your AI coding tools (Claude Code, Codex CLI, Gemini CLI, etc.) and queries their usage APIs. It never writes to or refreshes tokens that could invalidate your sessions.

Results are cached to `~/.cache/clawmeter/usage.json` with a 60-second TTL. The tray daemon writes the cache on every poll, so CLI invocations can read it without hitting the network.

### Platforms

| | Linux | macOS | Windows |
|---|---|---|---|
| CLI | amd64, arm64 | amd64, arm64 | amd64 |
| System tray | amd64, arm64 | amd64, arm64 | amd64 |

### Building from source

- Go 1.24+
- For tray builds: CGO + platform deps:
  - Linux: `libayatana-appindicator3-dev`
  - macOS: Xcode command line tools
  - Windows: GCC (e.g., via MSYS2)
