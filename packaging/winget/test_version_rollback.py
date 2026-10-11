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
printf '%s\n' "$*" >> "$GH_CALL_LOG"
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
            env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'], 'WINGET_DRY_RUN': '1', 'WINGET_WORK_ROOT': str(root), 'GH_CALL_LOG': str(root / 'gh-calls.log')}
            env.pop('GH_TOKEN', None)
            r = subprocess.run(['bash', str(SCRIPT), 'v1.2.9', 'manifest'], cwd=root, env=env, capture_output=True, text=True, timeout=10)
            self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
            self.assertIn('newer pending version', r.stderr)
            self.assertNotIn('unsafe-git-called', r.stderr)
            calls = (root / 'gh-calls.log').read_text().splitlines()
            self.assertTrue(any('pulls' in call for call in calls), calls)
            for call in calls:
                self.assertFalse(any(op in call for op in ['repo clone', 'pr edit', 'pr create', 'merge-upstream']), calls)

if __name__ == '__main__': unittest.main()
