from pathlib import Path
import os
import subprocess
import tempfile
import unittest

class ExtensionPackagingErrorsTest(unittest.TestCase):
    def run_packager_fixture(self, root, manifest, output):
        import sys

        scripts = root / "scripts"
        scripts.mkdir()
        script = scripts / "package-extension.py"
        script.write_bytes(Path(__file__).with_name(script.name).read_bytes())
        extension = root / "extension"
        extension.mkdir()
        (extension / "manifest.json").write_text(manifest)
        return subprocess.run(
            [sys.executable, "-I", "-B", str(script), str(output)],
            cwd=root,
            capture_output=True,
            text=True,
        )

    def test_missing_inputs_do_not_replace_a_previous_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            output = root / "previous.zip"
            output.write_bytes(b"synthetic previous archive")
            result = self.run_packager_fixture(
                root, '{"version":"0.0.0","icons":{"16":"missing.png"}}', output
            )
            self.assertEqual(result.returncode, 1)
            self.assertIn("missing from extension/:", result.stderr)
            self.assertIn("missing.png", result.stderr)
            self.assertEqual(output.read_bytes(), b"synthetic previous archive")

    def test_malformed_manifest_does_not_create_an_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            output = root / "unwritten.zip"
            result = self.run_packager_fixture(root, "{", output)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("JSONDecodeError", result.stderr)
            self.assertFalse(output.exists())


    def test_shared_icon_is_written_once_when_two_sizes_reference_it(self):
        import sys
        import zipfile

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scripts = root / "scripts"
            scripts.mkdir()
            script = scripts / "package-extension.py"
            script.write_bytes(Path(__file__).with_name(script.name).read_bytes())
            extension = root / "extension"
            extension.mkdir()
            (extension / "manifest.json").write_text(
                '{"version":"0.0.0","icons":{"16":"shared.png","32":"shared.png"}}'
            )
            for name in ("background.js", "console.js", "resets.js", "popup.html", "popup.js", "shared.png"):
                (extension / name).write_bytes(b"fixture")
            out = root / "archive.zip"
            result = subprocess.run(
                [sys.executable, "-I", "-B", str(script), str(out)], check=True, capture_output=True, text=True
            )
            self.assertNotIn("Duplicate name", result.stderr)
            with zipfile.ZipFile(out) as archive:
                self.assertEqual(archive.namelist().count("shared.png"), 1)
                self.assertEqual(archive.read("shared.png"), b"fixture")


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
sleep() { echo "sleep:$1" >> "$fixture/events"; SECONDS=$((SECONDS + $1)); }
'''
            home = root / 'home'
            home.mkdir()
            p = subprocess.run(['bash', '-c', harness + statements, 'fixture', str(library), str(root)], cwd=root, env={**os.environ, 'HOME': str(home)}, text=True, capture_output=True)
            events = (root/'events').read_text() if (root/'events').exists() else ''
            return p, events

    def test_fallback_runs_application_with_configured_budget(self):
        result, events = self.run_fixture('start_tcg_fallback')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('application-smoke', events)
        self.assertNotIn('sleep:60', events)

    def check_fallback_verdict(self, enabled, status):
        script = Path(__file__).with_name('windows-vm-smoke.sh').read_text()
        start = script.index('should_try_tcg() {')
        end = script.index('\n}', start) + 2
        return subprocess.run(
            ['bash', '-c', script[start:end] + '\nTCG_FALLBACK=$1; should_try_tcg "$2"',
             'fixture', str(enabled), str(status)],
            capture_output=True, text=True,
        )

    def test_disabled_fallback_does_not_retry_no_response_statuses(self):
        for status in (2, 4):
            with self.subTest(status=status):
                result = self.check_fallback_verdict(0, status)
                self.assertEqual(result.returncode, 1, result.stderr)

    def test_enabled_fallback_retries_only_no_response_statuses(self):
        for status, expected in ((0, 1), (1, 1), (2, 0), (3, 1), (4, 0)):
            with self.subTest(status=status):
                result = self.check_fallback_verdict(1, status)
                self.assertEqual(result.returncode, expected, result.stderr)

    def test_successful_fallback_clears_failed_kvm_status(self):
        result, _ = self.run_fixture('''RUN_VM=1
run_quota_preflight() { :; }; build_artifacts() { :; }; check_pe_subsystem() { :; }; run_wine_smoke() { :; }
start_quickemu_vm() { return 2; }; start_tcg_fallback() { return 0; }
kill_vm_processes() { :; }; cleanup_safe_vm_conf() { :; }
main''')
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_cli_smoke_failure_fails_fallback(self):
        result, events = self.run_fixture('''
probe_ssh_ready() { return 0; }
run_guest_cli_smoke() { echo application-smoke-failed >> "$fixture/events"; return 42; }
start_tcg_fallback
''')
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertIn('application-smoke-failed', events)

    def test_fallback_readiness_timeout_fails_within_budget(self):
        result, events = self.run_fixture('''
probe_ssh_ready() { return 1; }
start_tcg_fallback
''')
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('sleep:60', events)
        self.assertEqual(sum(int(line.split(':')[1]) for line in events.splitlines() if line.startswith('sleep:')), 2)

    def test_fallback_probes_stay_within_budget(self):
        result, events = self.run_fixture('''
probe_ssh_ready() { echo "probe:${1:-10}" >> "$fixture/events"; SECONDS=$((SECONDS + ${1:-10})); return 1; }
start_tcg_fallback
''')
        self.assertNotEqual(result.returncode, 0, result.stderr)
        spent = sum(int(line.split(':')[1]) for line in events.splitlines() if line.startswith(('sleep:', 'probe:')))
        self.assertLessEqual(spent, 2, events)

    def test_failed_kvm_and_failed_fallback_keep_main_failed(self):
        result, _ = self.run_fixture('''RUN_VM=1
run_quota_preflight() { :; }; build_artifacts() { :; }; check_pe_subsystem() { :; }; run_wine_smoke() { :; }
start_quickemu_vm() { return 2; }; start_tcg_fallback() { return 4; }
kill_vm_processes() { :; }; cleanup_safe_vm_conf() { :; }
main''')
        self.assertNotEqual(result.returncode, 0, result.stderr)


class ExtensionPackageTest(unittest.TestCase):
    def setUp(self):
        import json
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        scripts = self.root / 'scripts'
        scripts.mkdir()
        self.packager = scripts / 'package-extension.py'
        self.packager.write_bytes(Path(__file__).with_name('package-extension.py').read_bytes())
        self.extension = self.root / 'extension'
        self.extension.mkdir()
        self.manifest = self.extension / 'manifest.json'
        self.manifest.write_text(json.dumps({
            'version': '0.0.1', 'key': 'synthetic-fixture-marker', 'icons': {'16': 'icon.png'},
        }))
        for name in ('background.js', 'console.js', 'resets.js', 'popup.html', 'popup.js', 'icon.png', 'README.md'):
            (self.extension / name).write_bytes(b'synthetic fixture')

    def package(self, name):
        import sys
        archive = self.root / name
        result = subprocess.run(
            [sys.executable, str(self.packager), str(archive)],
            cwd=self.root, text=True, capture_output=True, timeout=10,
        )
        return result, archive

    def test_extension_archive_is_reproducible_and_omits_development_metadata(self):
        import json
        import zipfile
        first, first_path = self.package('first.zip')
        second, second_path = self.package('second.zip')
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(first_path.read_bytes(), second_path.read_bytes())
        with zipfile.ZipFile(first_path) as archive:
            self.assertEqual(set(archive.namelist()), {
                'manifest.json', 'background.js', 'console.js', 'resets.js', 'popup.html', 'popup.js', 'icon.png',
            })
            self.assertNotIn('key', json.loads(archive.read('manifest.json')))
        self.assertIn('key', json.loads(self.manifest.read_text()))

    def test_missing_manifest_icon_fails_without_writing_archive(self):
        (self.extension / 'icon.png').unlink()
        result, archive = self.package('failed.zip')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('missing from extension/: icon.png', result.stderr)
        self.assertFalse(archive.exists())

    def test_default_archive_uses_the_manifest_version_in_the_callers_directory(self):
        import sys
        caller = self.root / 'caller'
        caller.mkdir()
        result = subprocess.run(
            [sys.executable, '-I', '-B', str(self.packager)],
            cwd=caller, text=True, capture_output=True, timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ['clawmeter-extension-0.0.1.zip'])
        self.assertTrue((caller / 'clawmeter-extension-0.0.1.zip').is_file())
        self.assertFalse((self.root / 'clawmeter-extension-0.0.1.zip').exists())

    def test_packager_keeps_all_declared_icons_and_other_manifest_fields(self):
        import json
        import zipfile
        icons_dir = self.extension / 'icons'
        icons_dir.mkdir()
        (icons_dir / 'large.png').write_bytes(b'large fixture bytes')
        manifest = json.loads(self.manifest.read_text())
        manifest['icons']['128'] = 'icons/large.png'
        manifest['name'] = 'Fixture Package'
        self.manifest.write_text(json.dumps(manifest))
        result, target = self.package('multi-icon.zip')
        self.assertEqual(result.returncode, 0, result.stderr)
        with zipfile.ZipFile(target) as archive:
            self.assertEqual(archive.read('icon.png'), b'synthetic fixture')
            self.assertEqual(archive.read('icons/large.png'), b'large fixture bytes')
            self.assertEqual(json.loads(archive.read('manifest.json')),
                             {k: v for k, v in manifest.items() if k != 'key'})


if __name__ == '__main__': unittest.main()
