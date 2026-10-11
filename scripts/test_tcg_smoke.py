from pathlib import Path
import subprocess
import tempfile
import unittest

class TCGSmokeTest(unittest.TestCase):
    def run_fixture(self, statements):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            script = Path(__file__).with_name('windows-vm-smoke.sh').read_text()
            library = root / 'functions.sh'
            library.write_text(script.rsplit('main "$@"', 1)[0])
            (root / 'guest').mkdir()
            (root / 'guest/guest.sh').write_text(':')
            harness = '''library=$1; fixture=$2; shift 2
source "$library"
TCG_FALLBACK=1; BOOT_WAIT=2; PROBE_INTERVAL=1
vm_paths() { VM_BASE="$fixture"; VM_DIR="$fixture/guest"; VM_NAME=guest; }
cleanup_vm_runtime_files() { :; }
capture_screen() { :; }
probe_ssh_ready() { return 0; }
run_guest_cli_smoke() { echo application-smoke >> "$fixture/events"; return 0; }
sleep() { echo "sleep:$1" >> "$fixture/events"; }
'''
            p = subprocess.run(['bash', '-c', harness + statements, 'fixture', str(library), str(root)], cwd=root, text=True, capture_output=True)
            events = (root/'events').read_text() if (root/'events').exists() else ''
            return p, events

    def test_fallback_runs_application_with_configured_budget(self):
        result, events = self.run_fixture('start_tcg_fallback')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('application-smoke', events)
        self.assertNotIn('sleep:60', events)

    def test_successful_fallback_clears_failed_kvm_status(self):
        result, _ = self.run_fixture('''RUN_VM=1
run_quota_preflight() { :; }; build_artifacts() { :; }; check_pe_subsystem() { :; }; run_wine_smoke() { :; }
start_quickemu_vm() { return 2; }; start_tcg_fallback() { return 0; }
kill_vm_processes() { :; }; cleanup_safe_vm_conf() { :; }
main''')
        self.assertEqual(result.returncode, 0, result.stderr)

if __name__ == '__main__': unittest.main()
