#!/usr/bin/env bash
set -euo pipefail

source_file="${1:-$(cd "$(dirname "$0")" && pwd)/windows-vm-smoke.sh}"
[[ -f "$source_file" ]] || { printf 'source not found: %s\n' "$source_file" >&2; exit 2; }

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

extract_function() {
  local name="$1"
  awk -v name="$name" '
    !found && $0 ~ "^[[:space:]]*" name "[[:space:]]*\\(\\)[[:space:]]*\\{" {
      found=1
      print
      next
    }
    found {
      print
      if ($0 ~ /^[[:space:]]*}[[:space:]]*$/) exit
    }
    END { if (!found) exit 1 }
  ' "$source_file"
}

for function_name in have kill_vm_processes; do
  if ! extract_function "$function_name" >>"$tmp_dir/functions.sh"; then
    printf 'FAIL: %s is missing from %s\n' "$function_name" "$source_file" >&2
    exit 1
  fi
done
source "$tmp_dir/functions.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

vm_dir="$tmp_dir/vm config with spaces"
config_name="custom windows.conf"
config_path="$vm_dir/$config_name"
mkdir -p "$tmp_dir/bin" "$tmp_dir/no-quickemu" "$vm_dir"
cat >"$tmp_dir/bin/quickemu" <<'EOF'
#!/usr/bin/env bash
printf 'cwd=%s\n' "$PWD" >>"$QUICKEMU_LOG"
printf 'arg=<%s>\n' "$@" >>"$QUICKEMU_LOG"
exit "${QUICKEMU_STATUS:-0}"
EOF
cat >"$tmp_dir/bin/pkill" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$PKILL_LOG"
exit 0
EOF
chmod +x "$tmp_dir/bin/quickemu" "$tmp_dir/bin/pkill"

export QUICKEMU_LOG="$tmp_dir/quickemu.log"
export PKILL_LOG="$tmp_dir/pkill.log"
export PATH="$tmp_dir/bin:$PATH"
VM_CONF="$config_path"
: >"$VM_CONF"

kill_vm_processes "$vm_dir"
printf -v expected_call 'cwd=%s\narg=<--vm>\narg=<%s>\narg=<--kill>' "$vm_dir" "$config_name"
[[ "$(cat "$QUICKEMU_LOG")" == "$expected_call" ]] || fail 'quickemu was not called from the VM directory with the config basename'
[[ ! -s "$PKILL_LOG" ]] || fail 'broad pkill fallback was invoked'

: >"$QUICKEMU_LOG"
kill_vm_processes ""
[[ ! -s "$QUICKEMU_LOG" && ! -s "$PKILL_LOG" ]] || fail 'empty VM base triggered cleanup'

: >"$QUICKEMU_LOG"
VM_CONF="$vm_dir/missing windows.conf"
kill_vm_processes "$vm_dir"
[[ ! -s "$QUICKEMU_LOG" && ! -s "$PKILL_LOG" ]] || fail 'cleanup ran when the config was absent'

: >"$QUICKEMU_LOG"
VM_CONF="$config_path"
QUICKEMU_STATUS=1
kill_vm_processes "$vm_dir"
[[ -s "$QUICKEMU_LOG" && ! -s "$PKILL_LOG" ]] || fail 'quickemu failure triggered a fallback'
QUICKEMU_STATUS=0

: >"$QUICKEMU_LOG"
saved_path="$PATH"
PATH="$tmp_dir/no-quickemu"
kill_vm_processes "$vm_dir"
PATH="$saved_path"
[[ ! -s "$QUICKEMU_LOG" && ! -s "$PKILL_LOG" ]] || fail 'cleanup ran when quickemu was missing'

printf 'PASS: scoped VM cleanup (%s)\n' "$source_file"
