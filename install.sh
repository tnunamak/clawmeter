#!/bin/sh
set -eu

REPO="tnunamak/clawmeter"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="clawmeter"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)      echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Get latest release tag
LATEST="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
if [ -z "$LATEST" ]; then
  echo "Failed to determine latest release" >&2
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/${LATEST}/${BINARY}-${OS}-${ARCH}"

echo "Installing ${BINARY} ${LATEST} (${OS}/${ARCH}) to ${INSTALL_DIR}"

curl -fsSL "$URL" -o "/tmp/${BINARY}"
chmod +x "/tmp/${BINARY}"

# Clear macOS quarantine flag
if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "/tmp/${BINARY}" 2>/dev/null || true
fi

# Install — use sudo if needed
if [ -w "$INSTALL_DIR" ]; then
  mv "/tmp/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  echo "Need sudo to install to ${INSTALL_DIR}"
  sudo mv "/tmp/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

echo "${BINARY} ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"
