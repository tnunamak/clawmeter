from pathlib import Path
import os
import subprocess
import tempfile
import time
import unittest

SCRIPT = Path(__file__).with_name('build-inno.ps1')

class InnoStagingTest(unittest.TestCase):
    def test_concurrent_build_keeps_own_executable(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            compiler = root / 'ISCC.exe'
            compiler.write_text('''#!/bin/bash
for arg in "$@"; do
 case "$arg" in /DSourceDir=*) stage="${arg#*=}";; /DOutputDir=*) out="${arg#*=}";; /DAppVersion=*) version="${arg#*=}";; esac
done
printf '%s' "$stage" > "$TEST_ROOT/stage-$version"
if [[ "$version" == A ]]; then
 touch "$TEST_ROOT/A-ready"
 for i in {1..200}; do [[ -f "$TEST_ROOT/B-done" ]] && break; sleep .05; done
fi
cp "$stage/clawmeter.exe" "$out/ClawmeterSetup.exe"
if [[ "$version" == B ]]; then touch "$TEST_ROOT/B-done"; fi
''')
            compiler.chmod(0o755)
            for version in ['A', 'B']:
                (root / f'{version}.exe').write_text(version)
            def command(version):
                return ['pwsh', '-NoProfile', '-File', str(SCRIPT), '-BinaryPath', str(root / f'{version}.exe'),
                        '-Version', version, '-OutputDir', str(root / version), '-CompilerPath', str(compiler)]
            env = {**os.environ, 'TEST_ROOT': str(root)}
            a = subprocess.Popen(command('A'), env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                deadline = time.monotonic() + 15
                while not (root / 'A-ready').exists() and a.poll() is None and time.monotonic() < deadline:
                    time.sleep(.05)
                self.assertTrue((root / 'A-ready').exists(), 'first build did not reach compiler')
                b = subprocess.run(command('B'), env=env, capture_output=True, text=True, timeout=20)
                self.assertEqual(b.returncode, 0, b.stdout + b.stderr)
                stdout, stderr = a.communicate(timeout=20)
                self.assertEqual(a.returncode, 0, stdout + stderr)
                self.assertEqual((root / 'A/ClawmeterSetup.exe').read_text(), 'A')
                self.assertEqual((root / 'B/ClawmeterSetup.exe').read_text(), 'B')
                self.assertFalse(Path((root / 'stage-A').read_text()).exists(), 'first staging directory leaked')
                self.assertFalse(Path((root / 'stage-B').read_text()).exists(), 'second staging directory leaked')
            finally:
                if a.poll() is None: a.kill()
                a.communicate()

if __name__ == '__main__': unittest.main()
