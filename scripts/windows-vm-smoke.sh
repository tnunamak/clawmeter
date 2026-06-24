#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/windows-vm-smoke.sh [options]

Build and smoke-test Clawmeter's Windows binary from Linux.

Default:
  - builds release-like Windows artifact
  - checks PE subsystem with objdump
  - runs a weak Wine stdout smoke test when wine is installed
  - does NOT boot the VM unless --vm is passed

Options:
  --vm                         Boot/probe the Quickemu Windows VM
  --try-sudo-msrs              Try to set kvm.ignore_msrs=1 before booting
  --tcg-fallback               If KVM boot is blocked, try slow QEMU TCG fallback
  --vm-conf PATH               Quickemu config (default: ~/quickemu-vms/windows-11.conf)
  --vm-ram SIZE                RAM for temporary smoke-test VM config (default: 8G)
  --vm-cpu-cores N             CPU cores for temporary smoke-test VM config (default: 4)
  --min-available-gib N        Required headroom beyond VM RAM before boot (default: 24)
  --unsafe-vm-memory           Skip the memory preflight gate
  --ssh-user USER              SSH username to try (default: tnunamak)
  --ssh-port PORT              Host SSH forward port (default: 22220)
  --boot-wait SECONDS          Total VM probe time (default: 360)
  --probe-interval SECONDS     Probe interval (default: 15)
  --out-dir PATH               Output dir (default: ~/.tmp/clawmeter-windows-smoke)
  --skip-wine                  Skip Wine smoke
  --skip-quota                 Skip clawmeter status --agent preflight
  --help                       Show this help

Notes:
  The VM path on Tim's machine is usually ~/quickemu-vms/windows-11.conf.
  Quickemu exposes the host share as \\10.0.2.4\qemu inside Windows.
EOF
}

log() {
  printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"
}

warn() {
  printf '[%s] WARN: %s\n' "$(date +%H:%M:%S)" "$*" >&2
}

die() {
  printf '[%s] ERROR: %s\n' "$(date +%H:%M:%S)" "$*" >&2
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

repo_root() {
  git rev-parse --show-toplevel 2>/dev/null || pwd
}

RUN_VM=0
TRY_SUDO_MSRS=0
TCG_FALLBACK=0
SKIP_WINE=0
SKIP_QUOTA=0
VM_CONF="${HOME}/quickemu-vms/windows-11.conf"
SAFE_VM_CONF=""
VM_RAM="8G"
VM_CPU_CORES=4
MIN_AVAILABLE_GIB=24
UNSAFE_VM_MEMORY=0
SSH_USER="${USER:-tnunamak}"
SSH_PORT=22220
BOOT_WAIT=360
PROBE_INTERVAL=15
OUT_DIR="${HOME}/.tmp/clawmeter-windows-smoke"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vm) RUN_VM=1; shift ;;
    --try-sudo-msrs) TRY_SUDO_MSRS=1; shift ;;
    --tcg-fallback) TCG_FALLBACK=1; shift ;;
    --vm-conf) VM_CONF="${2:?--vm-conf requires a path}"; shift 2 ;;
    --vm-ram) VM_RAM="${2:?--vm-ram requires a size like 8G}"; shift 2 ;;
    --vm-cpu-cores) VM_CPU_CORES="${2:?--vm-cpu-cores requires a number}"; shift 2 ;;
    --min-available-gib) MIN_AVAILABLE_GIB="${2:?--min-available-gib requires a number}"; shift 2 ;;
    --unsafe-vm-memory) UNSAFE_VM_MEMORY=1; shift ;;
    --ssh-user) SSH_USER="${2:?--ssh-user requires a value}"; shift 2 ;;
    --ssh-port) SSH_PORT="${2:?--ssh-port requires a value}"; shift 2 ;;
    --boot-wait) BOOT_WAIT="${2:?--boot-wait requires seconds}"; shift 2 ;;
    --probe-interval) PROBE_INTERVAL="${2:?--probe-interval requires seconds}"; shift 2 ;;
    --out-dir) OUT_DIR="${2:?--out-dir requires a path}"; shift 2 ;;
    --skip-wine) SKIP_WINE=1; shift ;;
    --skip-quota) SKIP_QUOTA=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

REPO="$(repo_root)"
cd "$REPO"

mkdir -p "$OUT_DIR"
REPORT="${OUT_DIR}/report.txt"
: >"$REPORT"

record() {
  printf '%s\n' "$*" >>"$REPORT"
}

cleanup_vm_runtime_files() {
  local vm_dir="$1"
  rm -f \
    "${vm_dir}/.lock" \
    "${vm_dir}/windows-11.pid" \
    "${vm_dir}/windows-11.ports" \
    "${vm_dir}/windows-11.spice" \
    "${vm_dir}/windows-11-monitor.socket" \
    "${vm_dir}/windows-11.sock" \
    "${vm_dir}/windows-11-agent.sock" \
    "${vm_dir}/windows-11.swtpm-sock"
}

kill_vm_processes() {
  local vm_base="$1"
  if [[ -n "$vm_base" ]] && have quickemu && [[ -f "$VM_CONF" ]]; then
    (cd "$vm_base" && quickemu --vm "$(basename "$VM_CONF")" --kill >/dev/null 2>&1 || true)
  fi
  pkill -f 'qemu-system-x86_64.*windows-11' >/dev/null 2>&1 || true
  pkill -f 'swtpm.*windows-11' >/dev/null 2>&1 || true
}

pe_subsystem() {
  objdump -x "$1" | sed -n 's/.*(\(Windows [^)]*\)).*/\1/p' | head -1
}

run_quota_preflight() {
  [[ "$SKIP_QUOTA" -eq 1 ]] && return
  if have clawmeter; then
    log "Quota preflight: clawmeter status --agent"
    clawmeter status --agent | tee -a "$REPORT" || warn "quota preflight failed"
  else
    log "Quota preflight: go run ./cmd/clawmeter status --agent"
    go run ./cmd/clawmeter status --agent | tee -a "$REPORT" || warn "quota preflight failed"
  fi
}

build_artifacts() {
  have go || die "go is required"
  log "Building Windows artifacts into ${OUT_DIR}"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags tray \
    -o "${OUT_DIR}/clawmeter-console.exe" ./cmd/clawmeter
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags tray -ldflags="-H windowsgui" \
    -o "${OUT_DIR}/clawmeter-gui-old-shape.exe" ./cmd/clawmeter
  cp install.ps1 "${OUT_DIR}/install.ps1"
  ls -lh "${OUT_DIR}/clawmeter-console.exe" "${OUT_DIR}/clawmeter-gui-old-shape.exe" | tee -a "$REPORT"
}

check_pe_subsystem() {
  have objdump || die "objdump is required for PE subsystem check"
  local console_subsystem gui_subsystem
  console_subsystem="$(pe_subsystem "${OUT_DIR}/clawmeter-console.exe")"
  gui_subsystem="$(pe_subsystem "${OUT_DIR}/clawmeter-gui-old-shape.exe")"
  log "PE subsystem: console=${console_subsystem}, old_gui=${gui_subsystem}"
  record "PE subsystem: console=${console_subsystem}, old_gui=${gui_subsystem}"
  [[ "$console_subsystem" == "Windows CUI" ]] || die "release-like binary is not Windows CUI"
  [[ "$gui_subsystem" == "Windows GUI" ]] || warn "old-shape GUI comparison binary was not Windows GUI"
}

run_wine_smoke() {
  [[ "$SKIP_WINE" -eq 1 ]] && return
  if ! have wine; then
    warn "wine not installed; skipping Wine stdout smoke"
    return
  fi
  log "Running Wine stdout smoke"
  export WINEPREFIX="${OUT_DIR}/wineprefix"
  mkdir -p "$WINEPREFIX"
  timeout 90 wine "${OUT_DIR}/clawmeter-console.exe" --all --plain \
    >"${OUT_DIR}/wine-console.out" 2>"${OUT_DIR}/wine-console.err" || true
  local bytes
  bytes="$(wc -c <"${OUT_DIR}/wine-console.out")"
  log "Wine console stdout bytes=${bytes}"
  record "Wine console stdout bytes=${bytes}"
  sed -n '1,20p' "${OUT_DIR}/wine-console.out" | tee -a "$REPORT"
  [[ "$bytes" -gt 0 ]] || die "Wine smoke produced no stdout"
}

size_to_mib() {
  local value="$1"
  case "$value" in
    *[Gg])
      echo $(( ${value%[Gg]} * 1024 ))
      ;;
    *[Mm])
      echo "${value%[Mm]}"
      ;;
    *)
      die "unsupported size ${value}; use values like 8G or 8192M"
      ;;
  esac
}

mem_available_mib() {
  awk '/MemAvailable:/ {print int($2 / 1024)}' /proc/meminfo
}

swap_used_mib() {
  awk '
    /SwapTotal:/ {total=$2}
    /SwapFree:/ {free=$2}
    END {print int((total - free) / 1024)}
  ' /proc/meminfo
}

vm_memory_preflight() {
  local ram_mib available_mib required_mib swap_mib
  ram_mib="$(size_to_mib "$VM_RAM")"
  available_mib="$(mem_available_mib)"
  required_mib=$((ram_mib + MIN_AVAILABLE_GIB * 1024))
  swap_mib="$(swap_used_mib)"

  log "Memory preflight: vm_ram=${VM_RAM}, available=${available_mib}MiB, required=${required_mib}MiB, swap_used=${swap_mib}MiB"
  record "Memory preflight: vm_ram=${VM_RAM}, available=${available_mib}MiB, required=${required_mib}MiB, swap_used=${swap_mib}MiB"

  if [[ "$UNSAFE_VM_MEMORY" -eq 1 ]]; then
    warn "skipping memory gate because --unsafe-vm-memory was passed"
    return
  fi
  if [[ "$available_mib" -lt "$required_mib" ]]; then
    die "refusing to boot VM: MemAvailable ${available_mib}MiB < ${required_mib}MiB required (${VM_RAM} VM + ${MIN_AVAILABLE_GIB}GiB headroom)"
  fi
  if [[ "$swap_mib" -gt 2048 ]]; then
    die "refusing to boot VM: swap already has ${swap_mib}MiB in use; close memory-heavy apps or pass --unsafe-vm-memory"
  fi
}

vm_paths() {
  VM_BASE="$(cd "$(dirname "$VM_CONF")" && pwd)"
  VM_NAME="$(basename "$VM_CONF" .conf)"
  VM_DIR="${VM_BASE}/${VM_NAME}"
}

prepare_safe_vm_conf() {
  vm_paths
  local source_conf="$VM_CONF"
  local safe_conf="${VM_BASE}/${VM_NAME}-clawmeter-smoke.conf"

  awk -v ram="$VM_RAM" -v cpu="$VM_CPU_CORES" '
    BEGIN { saw_ram=0; saw_cpu=0 }
    /^ram=/ { print "ram=\"" ram "\""; saw_ram=1; next }
    /^cpu_cores=/ { print "cpu_cores=\"" cpu "\""; saw_cpu=1; next }
    { print }
    END {
      if (!saw_ram) print "ram=\"" ram "\""
      if (!saw_cpu) print "cpu_cores=\"" cpu "\""
    }
  ' "$source_conf" >"$safe_conf"

  SAFE_VM_CONF="$safe_conf"
  VM_CONF="$safe_conf"
  vm_paths
  log "Using temporary VM config ${VM_CONF} with ram=${VM_RAM}, cpu_cores=${VM_CPU_CORES}"
  record "Temporary VM config: ${VM_CONF}"
}

cleanup_safe_vm_conf() {
  if [[ -n "$SAFE_VM_CONF" ]]; then
    rm -f "$SAFE_VM_CONF"
  fi
}

copy_artifacts_to_vm_share() {
  vm_paths
  SHARE_DIR="${VM_BASE}/clawmeter-test"
  mkdir -p "$SHARE_DIR"
  cp "${OUT_DIR}/clawmeter-console.exe" "${SHARE_DIR}/clawmeter.exe"
  cp "${OUT_DIR}/install.ps1" "${SHARE_DIR}/install.ps1"
  log "Copied artifacts to ${SHARE_DIR}"
  record "Windows share path inside guest: \\\\10.0.2.4\\qemu\\clawmeter-test"
}

maybe_set_ignore_msrs() {
  if [[ ! -r /sys/module/kvm/parameters/ignore_msrs ]]; then
    warn "cannot read kvm.ignore_msrs; continuing"
    return
  fi
  local state
  state="$(cat /sys/module/kvm/parameters/ignore_msrs)"
  log "kvm.ignore_msrs=${state}"
  record "kvm.ignore_msrs=${state}"
  if [[ "$state" == "Y" || "$state" == "1" ]]; then
    return
  fi
  if [[ "$TRY_SUDO_MSRS" -eq 1 ]]; then
    log "Trying sudo tee /sys/module/kvm/parameters/ignore_msrs"
    if echo 1 | sudo tee /sys/module/kvm/parameters/ignore_msrs >/dev/null; then
      log "Set kvm.ignore_msrs=1"
      return
    fi
    warn "failed to set kvm.ignore_msrs with sudo"
  fi
  warn "Windows KVM boot may fail until you run: echo 1 | sudo tee /sys/module/kvm/parameters/ignore_msrs"
}

capture_screen() {
  local suffix="$1"
  vm_paths
  local ppm="${OUT_DIR}/screen-${suffix}.ppm"
  local png="${OUT_DIR}/screen-${suffix}.png"
  if [[ -S "${VM_DIR}/${VM_NAME}-monitor.socket" ]]; then
    printf 'screendump %s\n' "$ppm" | socat - "UNIX-CONNECT:${VM_DIR}/${VM_NAME}-monitor.socket" >/dev/null 2>&1 || true
    if [[ -s "$ppm" ]] && have convert; then
      convert "$ppm" "$png" >/dev/null 2>&1 || true
    fi
    [[ -s "$png" ]] && log "Captured screen: ${png}" || [[ -s "$ppm" ]] && log "Captured screen: ${ppm}"
  fi
}

run_ssh_command() {
  timeout 20 ssh \
    -o BatchMode=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o ConnectTimeout=5 \
    -p "$SSH_PORT" \
    "${SSH_USER}@127.0.0.1" \
    "$@"
}

probe_ssh_ready() {
  local out_file="${OUT_DIR}/ssh-probe.out"
  rm -f "$out_file"
  if timeout 10 ssh \
    -o BatchMode=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o ConnectTimeout=5 \
    -p "$SSH_PORT" \
    "${SSH_USER}@127.0.0.1" \
    'echo clawmeter-ssh-ready' >"$out_file" 2>&1; then
    grep -q 'clawmeter-ssh-ready' "$out_file"
    return
  fi
  if grep -Eiq 'permission denied|publickey|password|keyboard-interactive' "$out_file" 2>/dev/null; then
    return 2
  fi
  return 1
}

run_guest_cli_smoke() {
  local remote='powershell -NoProfile -ExecutionPolicy Bypass -Command "& \\10.0.2.4\qemu\clawmeter-test\clawmeter.exe --all --plain | Tee-Object -FilePath $env:TEMP\clawmeter-smoke.txt; if ((Get-Content -Raw $env:TEMP\clawmeter-smoke.txt).Trim().Length -eq 0) { exit 42 }"'
  log "Running guest CLI smoke over SSH"
  run_ssh_command "$remote" | tee -a "$REPORT"
}

start_quickemu_vm() {
  [[ -f "$VM_CONF" ]] || die "VM config not found: $VM_CONF"
  have quickemu || die "quickemu is required for --vm"
  have socat || die "socat is required for QEMU monitor screenshots"
  have nc || die "nc is required for SSH port probes"

  vm_memory_preflight
  prepare_safe_vm_conf
  copy_artifacts_to_vm_share
  maybe_set_ignore_msrs

  log "Cleaning stale VM runtime files"
  kill_vm_processes "$VM_BASE"
  cleanup_vm_runtime_files "$VM_DIR"

  local log_file="${OUT_DIR}/quickemu.log"
  log "Starting Quickemu VM: ${VM_CONF}"
  (
    cd "$VM_BASE"
    exec quickemu --vm "$(basename "$VM_CONF")" \
      --display none \
      --viewer none \
      --public-dir "$VM_BASE" \
      --ssh-port "$SSH_PORT"
  ) >"$log_file" 2>&1 &
  QUICKEMU_PID=$!
  record "quickemu_pid=${QUICKEMU_PID}"
  log "Quickemu launcher pid=${QUICKEMU_PID}; log=${log_file}"

  local waited=0
  local screenshot_count=0
  while [[ "$waited" -lt "$BOOT_WAIT" ]]; do
    sleep "$PROBE_INTERVAL"
    waited=$((waited + PROBE_INTERVAL))
    if ! pgrep -f 'qemu-system-x86_64.*windows-11' >/dev/null; then
      warn "QEMU is not running after ${waited}s"
      sed -n '1,160p' "$log_file" | tee -a "$REPORT"
      return 2
    fi
    if (( waited % 60 == 0 )); then
      screenshot_count=$((screenshot_count + 1))
      capture_screen "${screenshot_count}"
    fi
    if probe_ssh_ready; then
      log "SSH is ready on port ${SSH_PORT} after ${waited}s"
      if run_guest_cli_smoke; then
        log "Guest CLI smoke passed"
        return 0
      fi
      warn "SSH port is open, but command auth/execution failed"
      return 3
    else
      ssh_status=$?
      if [[ "$ssh_status" -eq 2 ]]; then
        warn "SSH answered, but ${SSH_USER} auth failed"
        sed -n '1,40p' "${OUT_DIR}/ssh-probe.out" | tee -a "$REPORT"
        return 3
      fi
    fi
    log "Waiting for guest SSH (${waited}/${BOOT_WAIT}s)"
  done
  warn "VM did not become SSH-reachable within ${BOOT_WAIT}s"
  capture_screen "final"
  return 4
}

start_tcg_fallback() {
  [[ "$TCG_FALLBACK" -eq 1 ]] || return 0
  vm_paths
  local generated="${VM_DIR}/${VM_NAME}.sh"
  [[ -f "$generated" ]] || {
    warn "Quickemu did not generate ${generated}; cannot run TCG fallback"
    return 0
  }
  log "Trying slow TCG fallback for ${BOOT_WAIT}s"
  cleanup_vm_runtime_files "$VM_DIR"
  perl -0pe \
    's/accel=kvm/accel=tcg/g; s/-cpu host,[^\n]+\\\\\n/    -cpu max \\\n/s; s/-m 32G/-m 8G/g; s/-display egl-headless,[^\n]+\\\\\n/    -display none \\\n/s; s/ 2>\/dev\/null\s*$/ 2>windows-11\/tcg.err/s' \
    "$generated" | (cd "$VM_BASE" && timeout "$BOOT_WAIT" bash) &
  local tcg_launcher=$!
  record "tcg_launcher_pid=${tcg_launcher}"
  sleep 60
  capture_screen "tcg"
  if probe_ssh_ready; then
    log "TCG SSH is ready"
  else
    warn "TCG SSH port did not become usable"
  fi
}

main() {
  log "Writing report to ${REPORT}"
  run_quota_preflight
  build_artifacts
  check_pe_subsystem
  run_wine_smoke

  local exit_status=0
  if [[ "$RUN_VM" -eq 1 ]]; then
    set +e
    start_quickemu_vm
    vm_status=$?
    set -e
    if [[ "$vm_status" -ne 0 ]]; then
      warn "Quickemu VM smoke did not complete successfully (status ${vm_status})"
      exit_status="$vm_status"
      start_tcg_fallback || true
    fi
    vm_paths
    kill_vm_processes "$VM_BASE"
    cleanup_vm_runtime_files "$VM_DIR"
    cleanup_safe_vm_conf
  fi

  log "Done. Report: ${REPORT}"
  return "$exit_status"
}

main "$@"
