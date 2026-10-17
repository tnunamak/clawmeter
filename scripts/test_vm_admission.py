"""Cooperative admission tests. These never start a real VM."""
from pathlib import Path
import os
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

    def test_second_admission_is_rejected(self):
        result = self.run_shell("""
admit_vm
# A second independent open-file description must not acquire this inode.
bash -c 'exec 9<"$1"; flock -n 9' child "$VM_BASE" && exit 99
""")
        self.assertEqual(result.returncode, 1, result.stderr)

    def test_budgets_and_occupied_port_fail_closed(self):
        for body in (
            "VM_CPU_CORES=5; admit_vm",
            "VM_RAM=9G; admit_vm",
            "VM_RAM=garbage; admit_vm",
            "nc() { return 0; }; admit_vm",
            'touch "$root/windows-clawmeter-smoke.conf"; admit_vm',
        ):
            with self.subTest(body=body):
                result = self.run_shell(body)
                self.assertNotEqual(result.returncode, 0, result.stderr)

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
