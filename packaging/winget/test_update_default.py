from pathlib import Path
import os
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('generate.sh')

class UpdateDefaultTest(unittest.TestCase):
    def test_generated_silent_installs_select_updates(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            asset = root / 'ClawmeterSetup.exe'
            asset.write_bytes(b'fixture executable')
            subprocess.run(['bash', str(SCRIPT), 'v1.2.3'], cwd=root,
                           env={**os.environ, 'WINGET_ASSET_PATH': str(asset)}, check=True, capture_output=True, timeout=10)
            manifest = (root / 'packaging/winget/out/manifests/t/tnunamak/Clawmeter/1.2.3/tnunamak.Clawmeter.installer.yaml').read_text()
            switches = [line for line in manifest.splitlines() if line.strip().startswith(('Silent:', 'SilentWithProgress:'))]
            self.assertEqual(len(switches), 2)
            for line in switches:
                self.assertIn('/TASKS=addtopath,updates', line)

if __name__ == '__main__': unittest.main()
