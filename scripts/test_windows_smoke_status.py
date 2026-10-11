from pathlib import Path
import os
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('windows-vm-smoke.sh')

class SmokeStatusTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.library = self.root / 'smoke.sh'
        self.library.write_text(SCRIPT.read_text().replace('main "$@"', ':'))

    def bash(self, command, extra=None):
        env = {**os.environ, 'HOME': str(self.root), 'PATH': str(self.root) + ':' + os.environ['PATH']}
        if extra: env.update(extra)
        return subprocess.run(['bash', '-c', 'library=$1; shift; source "$library"; ' + command, 'test', str(self.library)],
                              cwd=self.root, env=env, capture_output=True, text=True, timeout=15)

    def test_wine_partial_output_failure_is_rejected(self):
        for status in [7, 124]:
            with self.subTest(status=status):
                for name in ['timeout', 'wine']:
                    file = self.root / name
                    file.write_text(f'#!/bin/bash\necho partial-banner\nexit {status}\n')
                    file.chmod(0o755)
                r = self.bash('run_wine_smoke')
                self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_guest_partial_output_failure_is_propagated(self):
        remote = self.root / 'remote.txt'
        r = self.bash('run_ssh_command() { printf "%s" "$1" > "$CAPTURE"; }; run_guest_cli_smoke', {'CAPTURE': str(remote)})
        self.assertEqual(r.returncode, 0, r.stderr)
        cmd = remote.read_text().split('-Command "', 1)[1][:-1]
        fake = self.root / 'fixture.exe'
        fake.write_text('#!/bin/bash\necho partial-banner\nexit 7\n')
        fake.chmod(0o755)
        cmd = cmd.replace(r'\\10.0.2.4\qemu\clawmeter-test\clawmeter.exe', "'" + str(fake) + "'")
        r = subprocess.run(['pwsh', '-NoProfile', '-Command', cmd], env={**os.environ, 'TEMP': str(self.root)},
                           capture_output=True, text=True, timeout=15)
        self.assertEqual(r.returncode, 7, r.stdout + r.stderr)

if __name__ == '__main__': unittest.main()
