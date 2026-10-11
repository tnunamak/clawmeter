import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('submit-pr.sh')

class VersionRollbackTest(unittest.TestCase):
    def test_older_submission_cannot_replace_newer_pending_version(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            (root / 'manifest').mkdir()
            gh = root / 'gh'
            gh.write_text('''#!/bin/bash
if [[ "$*" == *contents/* ]]; then exit 0; fi
if [[ "$*" == *pulls* ]]; then
 printf '1\tNew version: tnunamak.Clawmeter version 1.2.10\thttps://example.invalid/1\tbranch-newer\n'; exit 0
fi
exit 99
''')
            gh.chmod(0o755)
            git = root / 'git'
            git.write_text('#!/bin/bash\necho unsafe-git-called >&2; exit 99\n')
            git.chmod(0o755)
            env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'], 'WINGET_DRY_RUN': '1', 'WINGET_WORK_ROOT': str(root)}
            env.pop('GH_TOKEN', None)
            r = subprocess.run(['bash', str(SCRIPT), 'v1.2.9', 'manifest'], cwd=root, env=env, capture_output=True, text=True, timeout=10)
            self.assertNotEqual(r.returncode, 0)
            self.assertIn('newer pending version', r.stderr)
            self.assertNotIn('unsafe-git-called', r.stderr)

if __name__ == '__main__': unittest.main()
