from pathlib import Path
import re
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).with_name("windows-vm-smoke.sh")
LIFECYCLE = SOURCE.with_name("lib") / "windows-vm-smoke-lifecycle.sh"
SCOPE_TEST = SOURCE.with_name("test-windows-vm-smoke-scope.sh")
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
    def test_scoped_cleanup_is_unchanged_by_lifecycle_formatting(self):
        # At the old ref the same definitions live in the CLI. Passing the
        # reformatted copy to the old scope fixture exposes its text parser.
        original = (LIFECYCLE if LIFECYCLE.is_file() else SOURCE).read_text()
        reformatted = original.replace(
            "kill_vm_processes() {", "kill_vm_processes()\n{", 1
        ).replace(
            '  local vm_base="$1"\n', '  {\n    local vm_base="$1"\n  }\n', 1
        )
        self.assertIn("kill_vm_processes()\n{\n  {", reformatted)
        with tempfile.TemporaryDirectory() as fixture:
            library = Path(fixture, "reformatted-lifecycle.sh")
            library.write_text(reformatted)
            process = subprocess.run(
                ["bash", str(SCOPE_TEST), str(library)],
                capture_output=True, text=True, timeout=5,
            )
        self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
        self.assertIn("PASS: scoped VM cleanup", process.stdout)

    def test_kvm_polling_uses_total_readiness_budget(self):
        # Source definitions only, never the host-level smoke entry point.
        trailer = """
rc=0
start_quickemu_vm || rc=$?
wait "$QUICKEMU_PID" || true
printf 'elapsed %s code %s\n' "$SECONDS" "$rc" >> "$fixture/trace"
"""
        with tempfile.TemporaryDirectory() as fixture:
            # Fake launcher and fake network probe. No VM or daemon is started.
            launcher = Path(fixture, "quickemu")
            launcher.write_text("#!/bin/sh\nexit 0\n")
            launcher.chmod(0o755)
            command = "\n".join(['export PATH="$1:$PATH"', 'source "$2"', HARNESS, trailer])
            process = subprocess.run(
                ["bash", "-c", command, "fixture", fixture, str(LIFECYCLE)],
                capture_output=True, text=True, timeout=5,
            )
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
