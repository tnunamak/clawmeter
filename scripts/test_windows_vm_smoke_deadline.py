from pathlib import Path
import re
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).with_name("windows-vm-smoke.sh")
HARNESS = r"""
set -euo pipefail
fixture="$1"
VM_CONF="$fixture/windows-11.conf"
VM_BASE="$fixture"
VM_DIR="$fixture"
VM_NAME=windows-11
OUT_DIR="$fixture"
REPORT="$fixture/report"
SSH_USER=fixture
SSH_PORT=2222
BOOT_WAIT=360
PROBE_INTERVAL=15
touch "$VM_CONF" "$REPORT"
unset SECONDS
SECONDS=0
have() { return 0; }
admit_vm() { :; }
vm_memory_preflight() { :; }
prepare_safe_vm_conf() { :; }
copy_artifacts_to_vm_share() { :; }
maybe_set_ignore_msrs() { :; }
defer_signals() { :; }
release_signals() { :; }
record() { :; }
log() { :; }
warn() { :; }
quickemu() { :; }
pgrep() { return 0; }
capture_screen() { :; }
sleep() { SECONDS=$((SECONDS + $1)); }
timeout() {
  limit="$1"
  printf 'probe %s %s\n' "$SECONDS" "$limit" >> "$fixture/trace"
  if (( limit < 10 )); then SECONDS=$((SECONDS + limit)); else SECONDS=$((SECONDS + 10)); fi
  return 124
}
run_guest_cli_smoke() { return 99; }
"""


class SmokeDeadlineTests(unittest.TestCase):
    def test_kvm_polling_uses_total_readiness_budget(self):
        source = SOURCE.read_text()
        # Execute only these two functions, never the host-level smoke entry point.
        parts = []
        for name in ["probe_ssh_ready", "start_quickemu_vm"]:
            match = re.search(rf"(?ms)^{name}\(\) \{{.*?^\}}$", source)
            self.assertIsNotNone(match, name)
            parts.append(match.group())
        trailer = """
rc=0
start_quickemu_vm || rc=$?
wait "$QUICKEMU_PID" || true
printf 'elapsed %s code %s\n' "$SECONDS" "$rc" >> "$fixture/trace"
"""
        with tempfile.TemporaryDirectory() as fixture:
            command = "\n".join([HARNESS, *parts, trailer])
            # Fake launcher and fake network probe. No VM or daemon is started.
            launcher = Path(fixture, "quickemu")
            launcher.write_text("#!/bin/sh\nexit 0\n")
            launcher.chmod(0o755)
            command = 'export PATH="$1:$PATH"\n' + command
            process = subprocess.run(["bash", "-c", command, "fixture", fixture],
                                     capture_output=True, text=True, timeout=5)
            self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
            trace = Path(fixture, "trace").read_text().splitlines()
        elapsed, code = map(int, re.fullmatch(r"elapsed (\d+) code (\d+)", trace[-1]).groups())
        probes = [tuple(map(int, line.split()[1:])) for line in trace[:-1]]
        self.assertEqual(code, 4)
        self.assertTrue(probes)
        self.assertLessEqual(elapsed, 360)
        self.assertTrue(all(start < 360 and limit <= 360 - start for start, limit in probes), probes)


if __name__ == "__main__":
    unittest.main()
