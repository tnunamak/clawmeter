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
  if ! "$@"; then err "command failed: $*"; exit 1; fi
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "need '$1' (command not found)"
    exit 1
  fi
}

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

# --- Find latest release with binaries ---

TMPDIR="$(mktemp -d)" || { err "failed to create temp directory"; exit 1; }
cleanup() { rm -rf "$TMPDIR"; }
trap cleanup EXIT

ASSET_NAME="${BINARY}-${OS}-${ARCH}"

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

# --- Install tray dependency on Linux ---

if [ "$OS" = "linux" ] && ! ldconfig -p 2>/dev/null | grep -q libayatana-appindicator3; then
  # Check if sudo is available without a password (avoid hanging when piped to sh)
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

# --- Start tray ---

say "Starting ${BINARY} tray..."
nohup "${INSTALL_DIR}/${BINARY}" tray >/dev/null 2>&1 &
say "Tray is running. It will auto-start on login from now on."
