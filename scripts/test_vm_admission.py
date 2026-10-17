"""Cooperative admission tests. These never start a real VM."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("windows-vm-smoke.sh")

class AdmissionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.library = self.root / "library.sh"
        self.library.write_text(SCRIPT.read_text().rsplit('main "$@"', 1)[0])
        self.env = {**os.environ, "HOME": str(self.root)}
        self.prefix = """
library=$1; root=$2; shift 2
source "$library"
VM_CONF="$root/windows.conf"
: > "$VM_CONF"
nc() { return 1; }
"""
    def run_shell(self, body):
        return subprocess.run(
            ["bash", "-c", self.prefix + body, "test",
             str(self.library), str(self.root)],
            cwd=self.root, env=self.env, capture_output=True,
            text=True, timeout=10)

    def test_output_directories_are_unique(self):
        a = self.run_shell('printf "%s" "$OUT_DIR"')
        b = self.run_shell('printf "%s" "$OUT_DIR"')
        self.assertEqual(a.returncode, 0, a.stderr)
        self.assertEqual(b.returncode, 0, b.stderr)
        self.assertNotEqual(a.stdout, b.stdout)

    def test_first_admission_succeeds(self):
        result = self.run_shell('admit_vm; echo ADMITTED')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("ADMITTED", result.stdout)

    def test_second_admission_is_rejected(self):
        result = self.run_shell("""
admit_vm
echo ADMITTED
# A second independent open-file description must not acquire this inode.
bash -c 'exec 9<"$1"; flock -n 9' child "$VM_BASE" || echo CONTENDER_REJECTED
""")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("ADMITTED", result.stdout)
        self.assertIn("CONTENDER_REJECTED", result.stdout)

    def test_budgets_and_occupied_port_fail_closed(self):
        for body, reason in (
            ("VM_CPU_CORES=5; admit_vm", "VM CPU budget"),
            ("VM_RAM=9G; admit_vm", "VM RAM budget"),
            ("VM_RAM=garbage; admit_vm", "invalid VM RAM"),
            ("nc() { return 0; }; admit_vm", "already occupied"),
            ('touch "$root/windows-clawmeter-smoke.conf"; admit_vm', "old smoke config"),
        ):
            with self.subTest(body=body):
                result = self.run_shell(body + "; echo ADMITTED")
                self.assertNotEqual(result.returncode, 0, result.stderr)
                self.assertNotIn("ADMITTED", result.stdout)
                self.assertIn(reason, result.stderr)

    def test_exit_cleanup_preserves_failure(self):
        result = self.run_shell("""
VM_OWNED=1; VM_BASE="$root"; VM_DIR="$root"
kill_vm_processes() { echo kill >> "$root/events"; }
cleanup_vm_runtime_files() { echo clean >> "$root/events"; }
cleanup_safe_vm_conf() { echo config >> "$root/events"; }
trap cleanup_run EXIT
exit 42
""")
        self.assertEqual(result.returncode, 42, result.stderr)
        self.assertEqual((self.root / "events").read_text(), "kill\nclean\nconfig\n")

    def test_cleanup_stops_the_launcher_it_started(self):
        result = self.run_shell("""
VM_OWNED=1; VM_BASE="$root"; VM_DIR="$root"
kill_vm_processes() { :; }
cleanup_vm_runtime_files() { :; }
cleanup_safe_vm_conf() { :; }
sleep 60 &
QUICKEMU_PID=$!
echo "$QUICKEMU_PID" > "$root/launcher.pid"
trap cleanup_run EXIT
exit 5
""")
        self.assertEqual(result.returncode, 5, result.stderr)
        pid = int((self.root / "launcher.pid").read_text())
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)

    def test_failed_copy_aborts_before_launch_and_leaves_no_stale_exe(self):
        result = self.run_shell("""
vm_paths
mkdir -p "$VM_BASE/clawmeter-test"
echo stale > "$VM_BASE/clawmeter-test/clawmeter.exe"
echo new > "$OUT_DIR/clawmeter-console.exe"; echo new > "$OUT_DIR/install.ps1"
chmod 555 "$VM_BASE/clawmeter-test"
if copy_artifacts_to_vm_share; then echo COPIED; else echo COPY_FAILED; fi
chmod 755 "$VM_BASE/clawmeter-test"
# Launch path: stub everything except the copy, and a quickemu that records a launch.
mkdir -p "$root/bin"; printf '#!/bin/sh\ntouch "%s/launched"\n' "$root" > "$root/bin/quickemu"; chmod +x "$root/bin/quickemu"
PATH="$root/bin:$PATH"
have() { return 0; }; vm_memory_preflight() { :; }; prepare_safe_vm_conf() { :; }
chmod 555 "$VM_BASE/clawmeter-test"
start_quickemu_vm && echo "START_STATUS=0" || echo "START_STATUS=$?"
chmod 755 "$VM_BASE/clawmeter-test"
""")
        self.assertIn("COPY_FAILED", result.stdout, result.stderr)
        self.assertIn("START_STATUS=1", result.stdout, result.stderr)
        self.assertFalse((self.root / "launched").exists())

    def test_successful_copy_replaces_a_stale_exe(self):
        result = self.run_shell("""
vm_paths
mkdir -p "$VM_BASE/clawmeter-test"; echo stale > "$VM_BASE/clawmeter-test/clawmeter.exe"
echo new > "$OUT_DIR/clawmeter-console.exe"; echo new > "$OUT_DIR/install.ps1"
copy_artifacts_to_vm_share && cat "$VM_BASE/clawmeter-test/clawmeter.exe"
""")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip().splitlines()[-1], "new")

    def test_tcg_fallback_only_after_a_silent_guest(self):
        result = self.run_shell("""
TCG_FALLBACK=1
for s in 2 3 4; do if should_try_tcg $s; then echo "$s yes"; else echo "$s no"; fi; done
TCG_FALLBACK=0; if should_try_tcg 4; then echo off-yes; else echo off-no; fi
""")
        self.assertEqual(result.stdout.split("\n")[:4], ["2 yes", "3 no", "4 yes", "off-no"])

    def test_tcg_fallback_stops_the_first_attempt_before_touching_state(self):
        result = self.run_shell("""
TCG_FALLBACK=1; BOOT_WAIT=1; PROBE_INTERVAL=1
vm_paths; mkdir -p "$VM_DIR"; echo 'sleep 5' > "$VM_DIR/$VM_NAME.sh"
stop_launcher() { echo stop_launcher >> "$root/events"; }
kill_vm_processes() { echo kill >> "$root/events"; }
cleanup_vm_runtime_files() { echo clean >> "$root/events"; }
probe_ssh_ready() { return 1; }; capture_screen() { :; }
start_tcg_fallback && echo "TCG_STATUS=0" || echo "TCG_STATUS=$?"
""")
        self.assertIn("TCG_STATUS=4", result.stdout, result.stderr)
        self.assertEqual((self.root / "events").read_text(), "stop_launcher\nkill\nclean\n")

    def test_interrupt_during_fallback_stops_its_whole_process_group(self):
        result = self.run_shell("""
VM_OWNED=1; VM_BASE="$root"; VM_DIR="$root"
kill_vm_processes() { :; }; cleanup_vm_runtime_files() { :; }; cleanup_safe_vm_conf() { :; }
(exec setsid sh -c 'sleep 60 & echo $! > "$0/child.pid"; wait' "$root") &
TCG_PID=$!
for i in 1 2 3 4 5 6 7 8 9 10; do [ -s "$root/child.pid" ] && break; sleep 0.1; done
cp "$root/child.pid" "$root/saved.pid"; echo "$TCG_PID" > "$root/leader.pid"
trap cleanup_run EXIT
exit 9
""")
        self.assertEqual(result.returncode, 9, result.stderr)
        for name in ("child.pid", "leader.pid"):
            pid = int((self.root / name).read_text())
            try:
                state = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[0]
            except FileNotFoundError:
                continue
            self.assertEqual(state, "Z", f"{name} still running")

    def test_interrupt_before_setsid_still_stops_the_owned_pid(self):
        # The fallback child exists but has not become a group leader yet:
        # `kill -- -PID` finds no group, so the PID itself must be signalled.
        result = self.run_shell("""
VM_OWNED=1; VM_BASE="$root"; VM_DIR="$root"
kill_vm_processes() { :; }; cleanup_vm_runtime_files() { :; }; cleanup_safe_vm_conf() { :; }
sleep 60 &
TCG_PID=$!
echo "$TCG_PID" > "$root/leader.pid"
trap cleanup_run EXIT
exit 9
""")
        self.assertEqual(result.returncode, 9, result.stderr)
        pid = int((self.root / "leader.pid").read_text())
        try:
            state = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[0]
        except FileNotFoundError:
            return
        self.assertEqual(state, "Z", "owned PID still running")

    def test_guest_smoke_fails_when_the_executable_is_unavailable(self):
        if not shutil.which("pwsh"):
            self.skipTest("pwsh not installed")
        body = self.run_shell("guest_smoke_body \"$root/missing.exe\"").stdout
        env = {**os.environ, "TEMP": str(self.root)}
        missing = subprocess.run(["pwsh", "-NoProfile", "-Command", body], env=env,
                                 capture_output=True, text=True, timeout=30)
        self.assertEqual(missing.returncode, 44, missing.stderr)

    def test_guest_smoke_fails_when_powershell_cannot_start_the_program(self):
        if not shutil.which("pwsh"):
            self.skipTest("pwsh not installed")
        not_executable = self.root / "clawmeter.exe"
        not_executable.write_text("not a program")
        body = self.run_shell(f"guest_smoke_body {not_executable}").stdout
        env = {**os.environ, "TEMP": str(self.root)}
        result = subprocess.run(["pwsh", "-NoProfile", "-Command", body], env=env,
                                capture_output=True, text=True, timeout=30)
        self.assertNotEqual(result.returncode, 0, result.stdout)

    def test_guest_smoke_passes_a_working_program_and_rejects_empty_output(self):
        if not shutil.which("pwsh"):
            self.skipTest("pwsh not installed")
        env = {**os.environ, "TEMP": str(self.root)}
        for script, expected in (("#!/bin/sh\necho ok\n", 0), ("#!/bin/sh\nexit 0\n", 42),
                                 ("#!/bin/sh\necho x\nexit 3\n", 3)):
            exe = self.root / "clawmeter.exe"
            exe.write_text(script)
            exe.chmod(0o755)
            body = self.run_shell(f"guest_smoke_body {exe}").stdout
            result = subprocess.run(["pwsh", "-NoProfile", "-Command", body], env=env,
                                    capture_output=True, text=True, timeout=30)
            with self.subTest(script=script):
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)

    def test_unowned_run_never_kills(self):
        result = self.run_shell("""
VM_OWNED=0
kill_vm_processes() { echo unexpected > "$root/killed"; }
cleanup_safe_vm_conf() { :; }
trap cleanup_run EXIT
exit 7
""")
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertFalse((self.root / "killed").exists())

if __name__ == "__main__":
    unittest.main()
