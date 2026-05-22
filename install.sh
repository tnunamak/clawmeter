#!/bin/sh
set -eu

REPO="tnunamak/clawmeter"
BINARY="clawmeter"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"
NO_MODIFY_PATH="${NO_MODIFY_PATH:-}"

# --- Helpers (rustup/uv pattern) ---

say() { printf "  %s\n" "$@"; }
warn() { printf "  \033[33mwarning:\033[0m %s\n" "$@" >&2; }
err() { printf "  \033[31merror:\033[0m %s\n" "$@" >&2; }

ensure() {
  if [ "$DRY_RUN" = "1" ]; then
    say "[dry-run] would run: $*"
    return 0
  fi
  if ! "$@"; then err "command failed: $*"; exit 1; fi
}

# Run a shell command string, or echo it in dry-run mode.
run() {
  if [ "$DRY_RUN" = "1" ]; then
    say "[dry-run] would run: $*"
    return 0
  fi
  eval "$@"
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "need '$1' (command not found)"
    exit 1
  fi
}

print_help() {
  cat <<EOF
Usage: install.sh [options]

Install or uninstall clawmeter.

By default this script downloads the latest clawmeter release binary into
\$INSTALL_DIR (default: ~/.local/bin), ensures that directory is on PATH,
and creates a normal app-launcher entry. It does NOT launch the tray,
enable launch-at-login, or install system packages unless you ask for it.
On Linux, the tray dependency (libayatana-appindicator3) is only installed
when --start or --autostart is passed, and only when passwordless sudo is
already available.

Options:
  --help                  Show this help and exit.
  --dry-run               Print what would happen without making changes.
                          No network downloads, no file writes, no package
                          installs, no tray launch.
  --start                 After install, start the tray daemon for this
                          session. Does NOT enable launch-at-login — pass
                          --autostart for that, or use the tray menu.
  --autostart             After install, enable launch-at-login by running
                          'clawmeter tray --install'. Can be combined with
                          --start to also launch the tray now.
  --no-modify-path        Do not edit shell rc files to add INSTALL_DIR to
                          PATH. Equivalent to setting NO_MODIFY_PATH=1.
  --uninstall             Remove the binary, app-launcher entry, autostart
                          entries, cache, and installer-added PATH lines.
                          Combine with --dry-run to preview the removals.
  -h                      Alias for --help.

Environment variables:
  INSTALL_DIR             Install location (default: ~/.local/bin).
  NO_MODIFY_PATH          If non-empty, do not edit shell rc files.

Examples:
  sh install.sh                          # install binary only
  sh install.sh --start                  # install + start tray now (no autostart)
  sh install.sh --autostart              # install + enable launch-at-login
  sh install.sh --start --autostart      # install, start now, and on every login
  sh install.sh --dry-run                # preview an install
  sh install.sh --uninstall              # remove everything
  sh install.sh --uninstall --dry-run    # preview uninstall
EOF
}

# --- Argument parsing (parse everything before any side effect) ---

DRY_RUN=0
DO_UNINSTALL=0
DO_START=0
DO_AUTOSTART=0
SHOW_HELP=0

for arg in "$@"; do
  case "$arg" in
    --help|-h)        SHOW_HELP=1 ;;
    --dry-run)        DRY_RUN=1 ;;
    --uninstall)      DO_UNINSTALL=1 ;;
    --start)          DO_START=1 ;;
    --autostart)      DO_AUTOSTART=1 ;;
    --no-modify-path) NO_MODIFY_PATH=1 ;;
    --)               ;;
    -*)
      err "unknown option: $arg"
      err "run 'install.sh --help' for usage"
      exit 2
      ;;
    *)
      err "unexpected argument: $arg"
      err "run 'install.sh --help' for usage"
      exit 2
      ;;
  esac
done

if [ "$SHOW_HELP" = "1" ]; then
  print_help
  exit 0
fi

if [ "$DRY_RUN" = "1" ]; then
  say "Dry run: no files will be written, no commands executed, no downloads made."
fi

# --- Uninstall ---

if [ "$DO_UNINSTALL" = "1" ]; then
  say "Uninstalling ${BINARY}..."
  if [ "$DRY_RUN" = "1" ]; then
    say "[dry-run] would stop any running '${BINARY}' process"
  else
    pkill -x "$BINARY" 2>/dev/null || true
  fi

  # Remove binary
  if [ -f "${INSTALL_DIR}/${BINARY}" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would remove ${INSTALL_DIR}/${BINARY}"
    else
      rm -f "${INSTALL_DIR}/${BINARY}"
      say "Removed ${INSTALL_DIR}/${BINARY}"
    fi
  fi

  # Remove macOS LaunchAgent
  _plist="${HOME}/Library/LaunchAgents/com.clawmeter.tray.plist"
  if [ -f "$_plist" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would unload and remove LaunchAgent ${_plist}"
    else
      launchctl unload "$_plist" 2>/dev/null || true
      rm -f "$_plist"
      say "Removed LaunchAgent"
    fi
  fi

  # Remove Linux autostart
  _desktop="${HOME}/.config/autostart/clawmeter.desktop"
  if [ -f "$_desktop" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would remove autostart entry ${_desktop}"
    else
      rm -f "$_desktop"
      say "Removed autostart entry"
    fi
  fi

  # Remove Linux app launcher and icon
  _desktop="${HOME}/.local/share/applications/clawmeter.desktop"
  if [ -f "$_desktop" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would remove app launcher ${_desktop}"
    else
      rm -f "$_desktop"
      say "Removed app launcher"
    fi
  fi
  for _icon in \
    "${HOME}/.local/share/pixmaps/clawmeter.png" \
    "${HOME}/.local/share/icons/hicolor/1024x1024/apps/clawmeter.png"
  do
    if [ ! -f "$_icon" ]; then
      continue
    fi
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would remove app icon ${_icon}"
    else
      rm -f "$_icon"
      say "Removed app icon"
    fi
  done

  # Remove macOS app launcher
  _app="${HOME}/Applications/Clawmeter.app"
  if [ -d "$_app" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would remove app launcher ${_app}"
    else
      rm -rf "$_app"
      say "Removed app launcher"
    fi
  fi

  # Remove PATH entry from shell rc files
  for _f in .bashrc .bash_profile .zshrc .zprofile .profile; do
    _rc="${HOME}/${_f}"
    if [ -f "$_rc" ] && grep -q "# Added by clawmeter installer" "$_rc" 2>/dev/null; then
      if [ "$DRY_RUN" = "1" ]; then
        say "[dry-run] would remove PATH entry from ${_rc}"
      else
        # Remove the comment line and the export line that follows it
        sed -i.bak '/# Added by clawmeter installer/{N;d;}' "$_rc" 2>/dev/null || \
          sed -i '' '/# Added by clawmeter installer/{N;d;}' "$_rc" 2>/dev/null
        rm -f "${_rc}.bak"
        say "Removed PATH entry from ${_rc}"
      fi
    fi
  done
  # Also check ZDOTDIR
  if [ -n "${ZDOTDIR:-}" ]; then
    for _f in .zshrc .zprofile; do
      _rc="${ZDOTDIR}/${_f}"
      if [ -f "$_rc" ] && grep -q "# Added by clawmeter installer" "$_rc" 2>/dev/null; then
        if [ "$DRY_RUN" = "1" ]; then
          say "[dry-run] would remove PATH entry from ${_rc}"
        else
          sed -i.bak '/# Added by clawmeter installer/{N;d;}' "$_rc" 2>/dev/null || \
            sed -i '' '/# Added by clawmeter installer/{N;d;}' "$_rc" 2>/dev/null
          rm -f "${_rc}.bak"
          say "Removed PATH entry from ${_rc}"
        fi
      fi
    done
  fi

  # Remove cache
  if [ -d "${HOME}/.cache/clawmeter" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      say "[dry-run] would remove ${HOME}/.cache/clawmeter"
    else
      rm -rf "${HOME}/.cache/clawmeter"
    fi
  fi

  say "Done."
  exit 0
fi

# --- Download helper (curl-first, wget fallback) ---

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    err "need curl or wget to download files"
    exit 1
  fi
}

try_download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    return 1
  fi
}

desktop_exec_quote() {
  _escaped="$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/`/\\`/g' -e 's/\$/\\$/g')"
  printf '"%s"' "$_escaped"
}

shell_single_quote() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

install_app_launcher() {
  case "$OS" in
    linux)
      _app_dir="${HOME}/.local/share/applications"
      _icon_dir="${HOME}/.local/share/pixmaps"
      _desktop="${_app_dir}/clawmeter.desktop"
      _icon="${_icon_dir}/clawmeter.png"
      _icon_url="https://raw.githubusercontent.com/${REPO}/${LATEST}/assets/icon-green-1024.png"

      ensure mkdir -p "$_app_dir" "$_icon_dir"
      if try_download "$_icon_url" "$_icon"; then
        say "Installed app icon to ${_icon}"
      else
        warn "could not install app icon; launcher will use desktop fallback"
        rm -f "$_icon"
      fi

      _exec="$(desktop_exec_quote "${INSTALL_DIR}/${BINARY}")"
      cat > "$_desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Clawmeter
Comment=AI usage monitor
Exec=${_exec} tray
Icon=${_icon}
Terminal=false
Categories=System;Monitor;
StartupNotify=false
EOF
      say "Installed app launcher to ${_desktop}"

      if command -v update-desktop-database >/dev/null 2>&1; then
        update-desktop-database "$_app_dir" >/dev/null 2>&1 || true
      fi
      ;;
    darwin)
      _app="${HOME}/Applications/Clawmeter.app"
      _contents="${_app}/Contents"
      _macos="${_contents}/MacOS"
      _launcher="${_macos}/Clawmeter"
      ensure mkdir -p "$_macos"
      _exec="$(shell_single_quote "${INSTALL_DIR}/${BINARY}")"
      cat > "$_launcher" <<EOF
#!/bin/sh
exec ${_exec} tray
EOF
      chmod +x "$_launcher"
      cat > "${_contents}/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>Clawmeter</string>
    <key>CFBundleIdentifier</key>
    <string>com.clawmeter.app</string>
    <key>CFBundleName</key>
    <string>Clawmeter</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSUIElement</key>
    <true/>
</dict>
</plist>
EOF
      say "Installed app launcher to ${_app}"
      ;;
  esac
}

# --- Detect OS ---

OS="$(uname -s)"
case "$OS" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)      err "Unsupported OS: $OS"; exit 1 ;;
esac

# --- Detect architecture ---

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       err "Unsupported architecture: $ARCH"; exit 1 ;;
esac

ASSET_NAME="${BINARY}-${OS}-${ARCH}"

# --- Dry run short-circuit ---
# In dry-run we describe what would happen without touching the network or disk.

if [ "$DRY_RUN" = "1" ]; then
  say "[dry-run] would resolve latest release of ${REPO} containing asset ${ASSET_NAME}"
  say "[dry-run] would download ${ASSET_NAME} to a temp directory and verify it"
  say "[dry-run] would install to ${INSTALL_DIR}/${BINARY} (sudo if not writable)"
  if [ -n "$NO_MODIFY_PATH" ]; then
    say "[dry-run] would NOT modify PATH (NO_MODIFY_PATH set / --no-modify-path)"
  else
    say "[dry-run] would ensure ${INSTALL_DIR} is on PATH via shell rc file"
  fi
  if [ "$OS" = "linux" ]; then
    say "[dry-run] would create app launcher ${HOME}/.local/share/applications/clawmeter.desktop"
    say "[dry-run] would install app icon ${HOME}/.local/share/pixmaps/clawmeter.png"
  elif [ "$OS" = "darwin" ]; then
    say "[dry-run] would create app launcher ${HOME}/Applications/Clawmeter.app"
  fi
  if [ "$OS" = "linux" ]; then
    if [ "$DO_START" = "1" ] || [ "$DO_AUTOSTART" = "1" ]; then
      say "[dry-run] on Linux: would install libayatana-appindicator3 via the system package manager if missing (requires passwordless sudo; gated on --start/--autostart)"
    else
      say "[dry-run] on Linux: would NOT install any system packages (no --start or --autostart given)"
    fi
  fi
  if [ "$DO_START" = "1" ]; then
    say "[dry-run] would launch '${BINARY} tray' in the background (this session only)"
  else
    say "[dry-run] would NOT start the tray (pass --start to launch it)"
  fi
  if [ "$DO_AUTOSTART" = "1" ]; then
    say "[dry-run] would enable launch-at-login via '${BINARY} tray --install'"
  else
    say "[dry-run] would NOT enable launch-at-login (pass --autostart to enable)"
  fi
  say "Done (dry-run)."
  exit 0
fi

# --- Find latest release with binaries ---

TMPDIR="$(mktemp -d)" || { err "failed to create temp directory"; exit 1; }
cleanup() { rm -rf "$TMPDIR"; }
trap cleanup EXIT

# Fetch recent releases (not just latest — latest may still be building)
download "https://api.github.com/repos/${REPO}/releases?per_page=5" "$TMPDIR/releases.json"

# Find the first release that has our binary asset
LATEST=""
URL=""
for tag in $(grep '"tag_name"' "$TMPDIR/releases.json" | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'); do
  _url="https://github.com/${REPO}/releases/download/${tag}/${ASSET_NAME}"
  # HEAD request to check if asset exists
  if command -v curl >/dev/null 2>&1; then
    if curl -fsSL --head "$_url" >/dev/null 2>&1; then
      LATEST="$tag"
      URL="$_url"
      break
    fi
  elif command -v wget >/dev/null 2>&1; then
    if wget --spider -q "$_url" 2>/dev/null; then
      LATEST="$tag"
      URL="$_url"
      break
    fi
  fi
done

if [ -z "$LATEST" ]; then
  err "no release found with binaries for ${OS}/${ARCH}"
  exit 1
fi

# --- Download binary ---

say "Installing ${BINARY} ${LATEST} (${OS}/${ARCH})..."

download "$URL" "$TMPDIR/${BINARY}"
ensure chmod +x "$TMPDIR/${BINARY}"

# Clear macOS quarantine flag
if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "$TMPDIR/${BINARY}" 2>/dev/null || true
fi

# Verify the binary works
if ! "$TMPDIR/${BINARY}" help >/dev/null 2>&1; then
  err "downloaded binary failed verification (${OS}/${ARCH})"
  exit 1
fi

# --- Install binary ---

ensure mkdir -p "$INSTALL_DIR"

# Stop existing tray instance if running
pkill -x "$BINARY" 2>/dev/null || true

if [ -w "$INSTALL_DIR" ]; then
  ensure mv "$TMPDIR/${BINARY}" "${INSTALL_DIR}/${BINARY}"
elif sudo -n true 2>/dev/null; then
  ensure sudo mv "$TMPDIR/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  err "${INSTALL_DIR} is not writable and sudo requires a password"
  err "re-run with: INSTALL_DIR=~/.local/bin sh install.sh"
  exit 1
fi

say "${BINARY} ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"

# --- Check for shadowing binaries ---

_shadow="$(command -v "$BINARY" 2>/dev/null || true)"
if [ -n "$_shadow" ] && [ "$_shadow" != "${INSTALL_DIR}/${BINARY}" ]; then
  warn "another ${BINARY} exists at ${_shadow} and may take precedence"
fi

# --- PATH setup (Homebrew OS-aware + uv idempotency pattern) ---

add_to_path() {
  # Already on PATH — nothing to do
  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) return ;;
  esac

  if [ -n "$NO_MODIFY_PATH" ]; then
    warn "${INSTALL_DIR} is not on PATH (NO_MODIFY_PATH is set, skipping)"
    return
  fi

  # Detect the right RC file based on OS + shell (Homebrew pattern)
  _rcfile=""
  _shell="$(basename "${SHELL:-sh}")"
  case "$_shell" in
    zsh)
      if [ "$OS" = "linux" ]; then
        _rcfile="${ZDOTDIR:-${HOME}}/.zshrc"
      else
        _rcfile="${ZDOTDIR:-${HOME}}/.zprofile"
      fi
      ;;
    bash)
      if [ "$OS" = "linux" ]; then
        _rcfile="${HOME}/.bashrc"
      else
        _rcfile="${HOME}/.bash_profile"
      fi
      ;;
    fish)
      _rcfile="${HOME}/.config/fish/conf.d/${BINARY}.fish"
      ;;
    *)
      _rcfile="${HOME}/.profile"
      ;;
  esac

  # If the chosen file doesn't exist, try common alternatives
  if [ "$_shell" != "fish" ] && [ ! -f "$_rcfile" ]; then
    for _f in .bashrc .bash_profile .zshrc .zprofile .profile; do
      if [ -f "${HOME}/${_f}" ]; then _rcfile="${HOME}/${_f}"; break; fi
    done
  fi
  : "${_rcfile:=${HOME}/.profile}"

  # Check if already present (idempotent)
  _line="export PATH=\"\$HOME/.local/bin:\$PATH\""
  if [ -f "$_rcfile" ] && grep -qF '.local/bin' "$_rcfile" 2>/dev/null; then
    return
  fi

  ensure mkdir -p "$(dirname "$_rcfile")"
  printf '\n# Added by clawmeter installer\n%s\n' "$_line" >> "$_rcfile"
  say "Added ${INSTALL_DIR} to PATH in ${_rcfile}"
  export PATH="${INSTALL_DIR}:$PATH"

  # Support GitHub Actions
  if [ -n "${GITHUB_PATH:-}" ]; then
    echo "$INSTALL_DIR" >> "$GITHUB_PATH"
  fi
}

add_to_path

# --- App launcher entry ---

install_app_launcher

# --- Install tray dependency on Linux ---
# Only runs when the user asked for tray-related side effects (--start or
# --autostart). A default install touches no system packages and never
# invokes sudo. Even when gated, we still require passwordless sudo so the
# curl|sh path never hangs on a password prompt.

if [ "$OS" = "linux" ] \
   && { [ "$DO_START" = "1" ] || [ "$DO_AUTOSTART" = "1" ]; } \
   && ! ldconfig -p 2>/dev/null | grep -q libayatana-appindicator3; then
  if sudo -n true 2>/dev/null; then
    say "Installing tray dependency (libayatana-appindicator3)..."
    if command -v apt-get >/dev/null 2>&1; then
      sudo apt-get install -y -qq libayatana-appindicator3-dev
    elif command -v dnf >/dev/null 2>&1; then
      sudo dnf install -y -q libayatana-appindicator3-gtk3-devel
    elif command -v pacman >/dev/null 2>&1; then
      sudo pacman -S --noconfirm libayatana-appindicator
    elif command -v zypper >/dev/null 2>&1; then
      sudo zypper install -y libayatana-appindicator3-devel
    elif command -v apk >/dev/null 2>&1; then
      sudo apk add libayatana-appindicator-dev
    fi
  else
    warn "libayatana-appindicator3 not found (needed for tray icon)"
    warn "install it with: sudo apt-get install -y libayatana-appindicator3-dev"
  fi
fi

# --- Enable launch-at-login (only if requested) ---

if [ "$DO_AUTOSTART" = "1" ]; then
  say "Enabling launch-at-login..."
  if ! "${INSTALL_DIR}/${BINARY}" tray --install; then
    warn "failed to enable launch-at-login"
  fi
fi

# --- Start tray (only if requested) ---

if [ "$DO_START" = "1" ]; then
  say "Starting ${BINARY} tray..."
  nohup "${INSTALL_DIR}/${BINARY}" tray >/dev/null 2>&1 &
  say "Tray started for this session."
fi

# --- Closing guidance ---

if [ "$DO_START" != "1" ] && [ "$DO_AUTOSTART" != "1" ]; then
  say "Binary and app launcher installed. To start the tray now, run: ${BINARY} tray"
  say "To enable launch-at-login, run: ${BINARY} tray --install"
  say "Or re-run this installer with --start and/or --autostart."
elif [ "$DO_START" = "1" ] && [ "$DO_AUTOSTART" != "1" ]; then
  say "Launch-at-login is NOT enabled. Run '${BINARY} tray --install' or re-run with --autostart to enable it."
fi
