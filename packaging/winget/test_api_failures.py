import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent

class ApiFailureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.cwd = Path(self.temp.name)
        (self.cwd / "manifest").mkdir()
        gh = self.cwd / "gh"
        gh.write_text("""#!/bin/bash
if [[ "$*" == *contents/* ]]; then
  if [[ "${LOOKUP_CODE:-200}" != 200 ]]; then
    echo "HTTP/2.0 $LOOKUP_CODE"; exit 1
  fi
  echo 'HTTP/2.0 200'; exit 0
fi
if [[ "$*" == 'pr list'* ]]; then exit 7; fi
if [[ "$*" == *pulls* ]]; then
  if [[ "${SUBMIT_FIXTURE:-}" == 1 ]]; then exit 0; fi
  exit 7
fi
if [[ "$*" == "repo clone "* && "${SUBMIT_FIXTURE:-}" == 1 ]]; then exit 0; fi
exit 99
""")
        gh.chmod(0o755)
        self.env = {**os.environ, "PATH": str(self.cwd) + ":" + os.environ["PATH"], "WINGET_DRY_RUN": "1"}
        self.env.pop("GH_TOKEN", None)

    def run_script(self, name, *args, code="200"):
        return subprocess.run(["bash", str(ROOT / name), *args], cwd=self.cwd,
                              env={**self.env, "LOOKUP_CODE": code}, text=True, capture_output=True, timeout=10)

    def test_cleanup_pr_list_failure_is_not_empty_success(self):
        r = self.run_script("close-superseded-first-package-prs.sh", "--dry-run")
        self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertNotIn("No open superseded", r.stdout)

    def test_cleanup_lookup_failure_is_not_absence(self):
        for code in ["401", "429", "503"]:
            with self.subTest(code=code):
                r = self.run_script("close-superseded-first-package-prs.sh", "--dry-run", code=code)
                self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertNotIn("not accepted", r.stdout)

    def test_confirmed_missing_package_still_skips_cleanup(self):
        r = self.run_script("close-superseded-first-package-prs.sh", "--dry-run", code="404")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("not accepted", r.stdout)

    def test_submit_lookup_failure_aborts_before_pr_lookup(self):
        r = self.run_script("submit-pr.sh", "v1.2.3", "manifest", code="503")
        self.assertEqual(r.returncode, 2, r.stdout + r.stderr)
        self.assertIn("upstream package lookup failed", r.stderr)

    def test_submit_dry_run_uses_installer_from_manifest(self):
        self.env.update(SUBMIT_FIXTURE="1", WINGET_WORK_ROOT=str(self.cwd / "work"))
        installer = "https://example.invalid/releases/clawmeter-portable.zip"
        (self.cwd / "manifest" / "tnunamak.Clawmeter.installer.yaml").write_text(
            "Installers:\n  - Architecture: x64\n    InstallerUrl: " + installer + "\n"
        )
        git = self.cwd / "git"
        git.write_text('#!/bin/bash\nif [[ "$*" == "diff --cached --quiet" ]]; then exit 1; fi\nexit 0\n')
        git.chmod(0o755)
        r = self.run_script("submit-pr.sh", "v1.2.3", "manifest")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn(f"- Installer: {installer}", r.stdout)
        self.assertNotIn("ClawmeterSetup.exe", r.stdout)


if __name__ == "__main__":
    unittest.main()
