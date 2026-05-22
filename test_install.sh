#!/bin/sh
# Safety tests for install.sh: ensure --help, --dry-run, and
# --uninstall --dry-run do not perform any side effects on the user's
# environment. All tests run under an isolated HOME / INSTALL_DIR.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALLER="${SCRIPT_DIR}/install.sh"

if [ ! -f "$INSTALLER" ]; then
  echo "install.sh not found at $INSTALLER" >&2
  exit 1
fi

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
pass() { printf 'PASS: %s\n' "$1"; }

# Sandbox setup
SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT

export HOME="$SANDBOX/home"
export INSTALL_DIR="$SANDBOX/install"
unset ZDOTDIR
mkdir -p "$HOME" "$INSTALL_DIR"

# Drop a sentinel rc file we DO NOT want touched
SENTINEL_RC="$HOME/.bashrc"
SENTINEL_CONTENT="# sentinel rc — should not be modified"
printf '%s\n' "$SENTINEL_CONTENT" > "$SENTINEL_RC"

# Snapshot the sandbox tree (paths + contents) so we can detect any change.
snapshot() {
  ( cd "$SANDBOX" && find . -type f -exec md5sum {} \; | sort )
}

BEFORE="$(snapshot)"

# --- Test 1: --help prints help and makes no changes, exit 0 ---
OUT="$(sh "$INSTALLER" --help 2>&1)"
echo "$OUT" | grep -q 'Usage: install.sh' || fail "--help did not print usage"
echo "$OUT" | grep -q -- '--dry-run' || fail "--help did not mention --dry-run"
echo "$OUT" | grep -q -- '--start' || fail "--help did not mention --start"
echo "$OUT" | grep -q -- '--autostart' || fail "--help did not mention --autostart"
AFTER="$(snapshot)"
[ "$BEFORE" = "$AFTER" ] || fail "--help modified the sandbox"
pass "--help prints usage and makes no changes"

# --- Test 2: -h alias ---
sh "$INSTALLER" -h >/dev/null 2>&1 || fail "-h should exit 0"
AFTER="$(snapshot)"
[ "$BEFORE" = "$AFTER" ] || fail "-h modified the sandbox"
pass "-h alias works"

# --- Test 3: --dry-run does not download, install, start, or modify PATH ---
OUT="$(sh "$INSTALLER" --dry-run 2>&1)"
echo "$OUT" | grep -qi 'dry run' || fail "--dry-run did not announce itself"
echo "$OUT" | grep -q 'would resolve latest release' || fail "--dry-run missing resolve message"
echo "$OUT" | grep -q 'would install to' || fail "--dry-run missing install message"
echo "$OUT" | grep -q 'would create app launcher' || fail "--dry-run missing app launcher message"
echo "$OUT" | grep -q 'would NOT start the tray' || fail "--dry-run should default to no start"
echo "$OUT" | grep -q 'would NOT enable launch-at-login' || fail "--dry-run should default to no autostart"
[ ! -f "$INSTALL_DIR/clawmeter" ] || fail "--dry-run created binary"
AFTER="$(snapshot)"
[ "$BEFORE" = "$AFTER" ] || fail "--dry-run modified the sandbox"
pass "--dry-run makes no changes and does not start tray or enable autostart"

# --- Test 4a: --dry-run --start announces start but not autostart ---
OUT="$(sh "$INSTALLER" --dry-run --start 2>&1)"
echo "$OUT" | grep -q 'would launch' || fail "--dry-run --start missing launch message"
echo "$OUT" | grep -q 'would NOT enable launch-at-login' || fail "--dry-run --start should not promise autostart"
[ ! -f "$INSTALL_DIR/clawmeter" ] || fail "--dry-run --start created binary"
AFTER="$(snapshot)"
[ "$BEFORE" = "$AFTER" ] || fail "--dry-run --start modified the sandbox"
pass "--dry-run --start announces start only (no autostart)"

# --- Test 4b: --dry-run --autostart announces autostart enable ---
OUT="$(sh "$INSTALLER" --dry-run --autostart 2>&1)"
echo "$OUT" | grep -q 'would enable launch-at-login' || fail "--dry-run --autostart missing autostart message"
echo "$OUT" | grep -q 'would NOT start the tray' || fail "--dry-run --autostart should not implicitly start tray"
AFTER="$(snapshot)"
[ "$BEFORE" = "$AFTER" ] || fail "--dry-run --autostart modified the sandbox"
pass "--dry-run --autostart announces autostart only"

# --- Test 4c: Linux sudo-package install is gated behind tray flags ---
# Force the OS detection branch to Linux by checking it directly. If we are
# not on Linux, skip this test rather than fake uname.
if [ "$(uname -s)" = "Linux" ]; then
  # Default install must NOT mention system package installation.
  OUT="$(sh "$INSTALLER" --dry-run 2>&1)"
  echo "$OUT" | grep -q 'would NOT install any system packages' \
    || fail "--dry-run (default) should disclaim system package install"
  if echo "$OUT" | grep -q 'would install libayatana-appindicator3'; then
    fail "--dry-run (default) must not claim it will install libayatana"
  fi
  # --start must surface that system packages may be installed.
  OUT="$(sh "$INSTALLER" --dry-run --start 2>&1)"
  echo "$OUT" | grep -q 'would install libayatana-appindicator3' \
    || fail "--dry-run --start should surface libayatana install"
  # --autostart must surface that system packages may be installed.
  OUT="$(sh "$INSTALLER" --dry-run --autostart 2>&1)"
  echo "$OUT" | grep -q 'would install libayatana-appindicator3' \
    || fail "--dry-run --autostart should surface libayatana install"
  AFTER="$(snapshot)"
  [ "$BEFORE" = "$AFTER" ] || fail "Linux dry-run sudo-gating tests modified the sandbox"
  pass "Linux system-package install is gated behind --start/--autostart"
else
  printf 'SKIP: Linux sudo-gating test (host is %s)\n' "$(uname -s)"
fi

# --- Test 4d: help mentions the no-default-sudo guarantee ---
OUT="$(sh "$INSTALLER" --help 2>&1)"
echo "$OUT" | grep -q 'install system' \
  || fail "--help should mention the no-system-package default"
echo "$OUT" | grep -q 'libayatana-appindicator3' \
  || fail "--help should explain the Linux tray dep gating"
echo "$OUT" | grep -q 'app-launcher entry' \
  || fail "--help should mention the default app launcher"
pass "--help documents the no-system-package default"

# --- Test 5: --uninstall --dry-run reports but does not remove ---
# Plant fake state that real uninstall would remove
mkdir -p "$INSTALL_DIR" "$HOME/.config/autostart" "$HOME/.cache/clawmeter" \
  "$HOME/.local/share/applications" "$HOME/.local/share/pixmaps" \
  "$HOME/.local/share/icons/hicolor/1024x1024/apps"
echo 'fake binary' > "$INSTALL_DIR/clawmeter"
echo 'fake desktop' > "$HOME/.config/autostart/clawmeter.desktop"
echo 'fake launcher' > "$HOME/.local/share/applications/clawmeter.desktop"
echo 'fake icon' > "$HOME/.local/share/pixmaps/clawmeter.png"
echo 'old fake icon' > "$HOME/.local/share/icons/hicolor/1024x1024/apps/clawmeter.png"
echo 'cached data' > "$HOME/.cache/clawmeter/usage.json"
cat >> "$SENTINEL_RC" <<'EOF'

# Added by clawmeter installer
export PATH="$HOME/.local/bin:$PATH"
EOF

BEFORE2="$(snapshot)"
OUT="$(sh "$INSTALLER" --uninstall --dry-run 2>&1)"
echo "$OUT" | grep -q 'would remove .*clawmeter' || fail "--uninstall --dry-run missing remove message"
echo "$OUT" | grep -q 'would remove app launcher' || fail "--uninstall --dry-run missing app launcher remove message"
echo "$OUT" | grep -q 'would remove PATH entry' || fail "--uninstall --dry-run missing PATH remove message"
# Files must still exist
[ -f "$INSTALL_DIR/clawmeter" ] || fail "--uninstall --dry-run removed binary"
[ -f "$HOME/.config/autostart/clawmeter.desktop" ] || fail "--uninstall --dry-run removed autostart"
[ -f "$HOME/.local/share/applications/clawmeter.desktop" ] || fail "--uninstall --dry-run removed app launcher"
[ -f "$HOME/.local/share/pixmaps/clawmeter.png" ] || fail "--uninstall --dry-run removed app icon"
[ -f "$HOME/.local/share/icons/hicolor/1024x1024/apps/clawmeter.png" ] || fail "--uninstall --dry-run removed old app icon"
[ -f "$HOME/.cache/clawmeter/usage.json" ] || fail "--uninstall --dry-run removed cache"
grep -q '# Added by clawmeter installer' "$SENTINEL_RC" || fail "--uninstall --dry-run removed PATH line"
AFTER2="$(snapshot)"
[ "$BEFORE2" = "$AFTER2" ] || fail "--uninstall --dry-run modified the sandbox"
pass "--uninstall --dry-run reports but does not remove"

# --- Test 6: unknown option exits non-zero and does nothing ---
BEFORE3="$(snapshot)"
if sh "$INSTALLER" --not-a-flag >/dev/null 2>&1; then
  fail "unknown flag should exit non-zero"
fi
AFTER3="$(snapshot)"
[ "$BEFORE3" = "$AFTER3" ] || fail "unknown flag modified the sandbox"
pass "unknown option exits non-zero and makes no changes"

printf '\nAll installer safety tests passed.\n'
