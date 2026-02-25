# Clawmeter

Check your Claude Pro/Max usage limits from the terminal.

Anthropic doesn't expose a public API for plan utilization — the only way to see your 5-hour and 7-day usage is in the Claude.ai UI. Clawmeter reads your Claude Code OAuth token and queries the usage endpoint so you can see where you stand without leaving the terminal.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh
```

Override the install directory:

```bash
INSTALL_DIR=~/.local/bin curl -fsSL https://raw.githubusercontent.com/tnunamak/clawmeter/main/install.sh | sh
```

### Other methods

```bash
# Go install
go install github.com/tnunamak/clawmeter/cmd/clawmeter@latest

# Build from source
git clone https://github.com/tnunamak/clawmeter.git
cd clawmeter
make install          # CLI only, pure Go
make install-tray     # with system tray support (requires CGO)
```

Tray prerequisites (Linux): `sudo apt install libayatana-appindicator3-dev`

## Usage

```
$ clawmeter
clawmeter  5h ███░░░░░░░░░░░░░░░░░  17%  resets 3h05m
           7d ████████████░░░░░░░░  60%  resets 1d7h
```

Colors: green <60%, yellow 60–80%, red >80%.

### Commands

```
clawmeter                  # show usage (default)
clawmeter status           # same as above
clawmeter status --plain   # no color, single line
clawmeter status --json    # full JSON output
clawmeter tray             # system tray mode (requires tray build)
clawmeter help             # show help
```

### Plain mode

Automatic when stdout isn't a TTY (pipes, scripts), or force with `--plain`:

```
5h: 17% (resets 3h05m)  7d: 60% (resets 1d7h)
```

### JSON mode

```json
{
  "usage": {
    "five_hour": {
      "utilization": 17,
      "resets_at": "2026-02-26T00:00:01.334232Z"
    },
    "seven_day": {
      "utilization": 60,
      "resets_at": "2026-02-27T04:00:00.334249Z"
    }
  }
}
```

### System tray

Build with `-tags tray` to get a persistent system tray icon:

- Icon color reflects max utilization (green/yellow/red)
- Polls every 5 minutes
- Desktop notifications on threshold crossings (80%, 95%)
- "Refresh Now" menu item for immediate update

## How it works

Clawmeter reads `~/.claude/.credentials.json` (written by Claude Code) and calls `GET https://api.anthropic.com/api/oauth/usage` with the OAuth bearer token. It never writes to the credentials file or refreshes the token — that would invalidate Claude Code's session.

Results are cached to `~/.cache/clawmeter/usage.json` with a 60-second TTL. The tray daemon writes the cache on every poll, so CLI invocations can read it without hitting the network.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | API or runtime error |
| 2 | Token missing or expired |

## Requirements

- An active Claude Code session (for the OAuth token)
- For building from source: Go 1.24+
- For tray builds: CGO + `libayatana-appindicator3-dev` (Linux) or Xcode (macOS)
