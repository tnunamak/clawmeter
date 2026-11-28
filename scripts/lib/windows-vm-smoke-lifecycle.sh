# shellcheck shell=bash
# Side-effect-free function definitions shared by the VM smoke CLI and fixtures.
# The caller supplies VM configuration, process state, and logging helpers.

have() {
  command -v "$1" >/dev/null 2>&1
}

kill_vm_processes() {
  local vm_base="$1"
  if [[ -n "$vm_base" ]] && have quickemu && [[ -f "$VM_CONF" ]]; then
    (cd "$vm_base" && quickemu --vm "$(basename "$VM_CONF")" --kill >/dev/null 2>&1 || true)
  fi
}

probe_ssh_ready() {
  local limit="${1:-10}"
  local out_file="${OUT_DIR}/ssh-probe.out"
  rm -f "$out_file"
  if timeout "$limit" ssh \
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

start_quickemu_vm() {
  [[ -f "$VM_CONF" ]] || die "VM config not found: $VM_CONF"
  have quickemu || die "quickemu is required for --vm"
  have socat || die "socat is required for QEMU monitor screenshots"
  have nc || die "nc is required for SSH port probes"

  admit_vm
  vm_memory_preflight || return 1
  prepare_safe_vm_conf || return 1
  copy_artifacts_to_vm_share || { warn "could not copy artifacts to the VM share"; return 1; }
  maybe_set_ignore_msrs

  # Never kill a pre-existing VM as a startup preflight.
  [[ ! -e "${VM_DIR}/.lock" && ! -e "${VM_DIR}/${VM_NAME}.pid" ]] ||
    die "VM runtime state already exists; inspect the disposable worker"

  local log_file="${OUT_DIR}/quickemu.log"
  log "Starting Quickemu VM: ${VM_CONF}"
  defer_signals
  (
    cd "$VM_BASE"
    exec quickemu --vm "$(basename "$VM_CONF")" \
      --display none \
      --viewer none \
      --public-dir "$VM_BASE" \
      --ssh-port "$SSH_PORT"
  ) >"$log_file" 2>&1 &
  QUICKEMU_PID=$!
  VM_OWNED=1
  release_signals
  record "quickemu_pid=${QUICKEMU_PID}"
  log "Quickemu launcher pid=${QUICKEMU_PID}; log=${log_file}"

  local started=$SECONDS deadline waited remaining delay
  deadline=$((started + BOOT_WAIT))
  local screenshot_count=0 next_screenshot=60
  while (( SECONDS < deadline )); do
    remaining=$((deadline - SECONDS))
    delay="$PROBE_INTERVAL"
    (( delay <= remaining )) || delay="$remaining"
    sleep "$delay"
    remaining=$((deadline - SECONDS))
    (( remaining > 0 )) || break
    waited=$((SECONDS - started))
    if ! pgrep -f 'qemu-system-x86_64.*windows-11' >/dev/null; then
      warn "QEMU is not running after ${waited}s"
      sed -n '1,160p' "$log_file" | tee -a "$REPORT"
      return 2
    fi
    if (( waited >= next_screenshot )); then
      screenshot_count=$((screenshot_count + 1))
      next_screenshot=$(( (waited / 60 + 1) * 60 ))
      capture_screen "${screenshot_count}"
    fi
    remaining=$((deadline - SECONDS))
    (( remaining > 0 )) || break
    (( remaining <= 10 )) || remaining=10
    if probe_ssh_ready "$remaining"; then
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
