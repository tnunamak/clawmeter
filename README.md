# Clawmeter

Check your Claude Pro/Max usage limits from the terminal and system tray.

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
# Go install (CLI only, no tray)
go install github.com/tnunamak/clawmeter/cmd/clawmeter@latest

# Build from source
git clone https://github.com/tnunamak/clawmeter.git
cd clawmeter
make install          # CLI only, pure Go
make install-tray     # with system tray support (requires CGO)
```

Tray prerequisites (Linux): `sudo apt install libayatana-appindicator3-dev`

## System tray

```bash
clawmeter tray
```

- Color-coded icon: green (<60%), yellow (60–80%), red (>80%), gray (token expired)
- Hover tooltip shows full usage summary
- Polls every 5 minutes with "Refresh Now" for immediate update
- Desktop notifications at 80% and 95% thresholds
- Usage projection shows whether you're on track to hit limits
- "Launch at login" toggle in the menu
- "Open Claude Code to reauth" when token expires

### Launch at login

Toggle from the tray menu, or via CLI:

```bash
clawmeter tray --install    # enable
clawmeter tray --uninstall  # disable
```

## CLI

```
$ clawmeter
clawmeter  5h ███░░░░░░░░░░░░░░░░░  17%  resets 3h05m  ✓ on track
           7d ████████████░░░░░░░░  60%  resets 1d7h   ✓ on track
```

Colors: green <60%, yellow 60–80%, red >80%.

### Commands

```
clawmeter                  # show usage (default)
clawmeter status --plain   # no color, single line
clawmeter status --json    # full JSON with forecast
clawmeter tray             # system tray mode
clawmeter help             # show help
```

### Plain mode

Automatic when stdout isn't a TTY (pipes, scripts), or force with `--plain`:

```
5h: 17% (resets 3h05m, on track)  7d: 60% (resets 1d7h, on track)
```

## How it works

Clawmeter reads your Claude Code OAuth credentials and calls `GET https://api.anthropic.com/api/oauth/usage` with the bearer token. Credentials are read from (in order):

1. `CLAUDE_CODE_OAUTH_TOKEN` environment variable
2. macOS Keychain (`Claude Code-credentials`)
3. `~/.claude/.credentials.json` (Linux)

It never writes to or refreshes the token — that would invalidate Claude Code's session.

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
