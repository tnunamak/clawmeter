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
            write_installer_manifest(root)
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

    def test_older_submission_does_not_replace_unrecognized_pending_version(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            write_installer_manifest(root)
            gh = root / 'gh'
            gh.write_text('''#!/bin/bash
printf '%s\n' "$*" >> "$GH_CALL_LOG"
if [[ "$*" == *contents/* ]]; then exit 0; fi
if [[ "$*" == *pulls* ]]; then
 printf '1\tNew version: tnunamak.Clawmeter version 1.2.10-preview\thttps://example.invalid/1\tbranch-newer\n'; exit 0
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
            self.assertIn('unrecognized version', r.stderr)
            self.assertNotIn('unsafe-git-called', r.stderr)
            calls = (root / 'gh-calls.log').read_text().splitlines()
            self.assertTrue(any('pulls' in call for call in calls), calls)
            for call in calls:
                self.assertFalse(any(op in call for op in ['repo clone', 'pr edit', 'pr create', 'merge-upstream']), calls)

def write_installer_manifest(root, version='1.2.9', package='tnunamak.Clawmeter',
                             urls=('https://example.invalid/releases/SelectedInstaller.exe',)):
    directory = root / 'manifest'
    directory.mkdir(exist_ok=True)
    lines = [f'PackageIdentifier: {package}', f'PackageVersion: {version}', 'Installers:']
    for url in urls:
        lines.extend(['- Architecture: x64', f'  InstallerUrl: {url}'])
    lines.extend(['ManifestType: installer', 'ManifestVersion: 1.6.0'])
    (directory / 'tnunamak.Clawmeter.installer.yaml').write_text('\n'.join(lines) + '\n')


class SelectedInstallerTest(unittest.TestCase):
    def simulate_submission(self, root):
        calls = root / 'calls'
        gh = root / 'gh'
        gh.write_text("""#!/bin/bash
set -eu
printf 'gh %s\\n' "$*" >> "$CALLS"
case "$1 $2" in
  'api --include'|'api --method'|'auth setup-git'|'repo clone') exit 0 ;;
  'pr create')
    while [[ $# -gt 0 ]]; do
      if [[ "$1" == --body ]]; then printf '%s' "$2" > "$BODY"; exit 0; fi
      shift
    done
    exit 90 ;;
  api\\ repos/*/pulls) exit 0 ;;
  *) exit 91 ;;
esac
""")
        git = root / 'git'
        git.write_text("""#!/bin/bash
printf 'git %s\\n' "$*" >> "$CALLS"
case "$1" in
  sparse-checkout|remote|fetch|checkout|add|commit|push) exit 0 ;;
  diff) exit 1 ;;
  *) exit 92 ;;
esac
""")
        gh.chmod(0o755)
        git.chmod(0o755)
        env = {'PATH': str(root) + ':' + os.defpath, 'HOME': str(root),
               'CALLS': str(calls), 'BODY': str(root / 'body'),
               'WINGET_WORK_ROOT': str(root), 'WINGET_KEEP_WORKTREE': '1',
               'GH_TOKEN': 'offline-fixture-placeholder'}
        result = subprocess.run(['bash', str(SCRIPT), 'v1.2.9', 'manifest'], cwd=root,
                                env=env, capture_output=True, text=True, timeout=10)
        return result, calls

    def test_exact_selected_installer_url_in_mocked_pr_body(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            url = 'https://example.invalid/releases/SelectedInstaller.exe'
            write_installer_manifest(root, urls=(url,))
            result, calls = self.simulate_submission(root)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn(f'- Installer: {url}', (root / 'body').read_text().splitlines())
            self.assertIn('gh pr create ', calls.read_text())
            self.assertNotIn('ClawmeterSetup.exe', (root / 'body').read_text())

    def test_url_outside_list_or_missing_from_an_entry_is_refused_before_git_or_gh(self):
        url = 'https://example.invalid/releases/Installer.exe'
        for installers in (
            f'InstallerUrl: {url}\nInstallers: []',
            f'Installers:\n- Architecture: x64\n  InstallerUrl: {url}\n- Architecture: arm64',
        ):
            with self.subTest(installers=installers), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                write_installer_manifest(root)
                fixture = root / 'manifest' / 'tnunamak.Clawmeter.installer.yaml'
                fixture.write_text(
                    'PackageIdentifier: tnunamak.Clawmeter\nPackageVersion: 1.2.9\n'
                    f'{installers}\nManifestType: installer\nManifestVersion: 1.6.0\n'
                )
                result, calls = self.simulate_submission(root)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn('installer manifest', result.stderr)
                self.assertFalse(calls.exists())

    def test_ambiguous_or_mismatched_installer_manifest_refuses_before_mocked_git_or_gh(self):
        cases = [
            {'urls': ('https://example.invalid/first.exe', 'https://example.invalid/other.exe')},
            {'version': '1.2.8'},
            {'package': 'another.Project'},
        ]
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                write_installer_manifest(root, **case)
                result, calls = self.simulate_submission(root)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn('installer manifest', result.stderr)
                self.assertFalse(calls.exists(), 'reject before querying or making Git/PR changes')


if __name__ == '__main__': unittest.main()
