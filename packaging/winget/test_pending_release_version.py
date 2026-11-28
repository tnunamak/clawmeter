"""WinGet pending version must come from the selected immutable PR head."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("submit-pr.sh").resolve()
PACKAGE = "tnunamak.Clawmeter"
HEAD = "a" * 40


class PendingVersionTest(unittest.TestCase):
    def run_fixture(self, mode="one"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifests = root / "manifests"
            manifests.mkdir()
            (manifests / f"{PACKAGE}.installer.yaml").write_text(
                f"PackageIdentifier: {PACKAGE}\nPackageVersion: 1.2.10\n"
                "ManifestType: installer\nInstallers:\n"
                "- Architecture: x64\n  InstallerUrl: https://example.invalid/setup.exe\n"
            )
            gh = root / "gh"
            gh.write_text("""#!/usr/bin/env python3
import os,sys
from pathlib import Path
args=sys.argv[1:]
with open(os.environ["FIXTURE_LOG"],"a") as out: print("gh",*args,file=out)
mode=os.environ["FIXTURE_MODE"]
path="manifests/t/tnunamak/Clawmeter/1.2.9/tnunamak.Clawmeter.installer.yaml"
if args[:2]==["repo","clone"]:
    Path(args[3]).mkdir(parents=True,exist_ok=True)
elif "/pulls/7/files" in " ".join(args):
    print(path)
    if mode=="ambiguous": print(path.replace("1.2.9","1.2.8"))
elif "/contents/"+path in " ".join(args):
    assert "?ref="+"a"*40 in " ".join(args)
    print("PackageIdentifier:", "wrong.Package" if mode=="wrong" else "tnunamak.Clawmeter")
    print("PackageVersion: 1.2.9")
    print("ManifestType: installer")
elif "/pulls/7" in " ".join(args):
    print("a"*40)
elif "/pulls" in " ".join(args):
    print("7\\tNew version: tnunamak.Clawmeter version 1.2.9 (ready for review)\\thttps://example.invalid/pr/7\\tfixture-branch\\t"+"a"*40)
elif "/contents/manifests/t/tnunamak/Clawmeter" in " ".join(args):
    print("HTTP/2 200")
else:
    raise SystemExit(1)
""")
            git = root / "git"
            git.write_text("""#!/usr/bin/env bash
printf 'git %s\\n' "$*" >> "$FIXTURE_LOG"
if [[ "$1" == diff ]]; then exit 1; fi
""")
            for executable in (gh, git):
                executable.chmod(0o700)
            log = root / "calls"
            result = subprocess.run(
                ["bash", str(SCRIPT), "v1.2.10", str(manifests)],
                env={**os.environ, "PATH": f"{root}:{os.environ['PATH']}",
                     "WINGET_DRY_RUN": "1", "WINGET_WORK_ROOT": str(root),
                     "FIXTURE_LOG": str(log), "FIXTURE_MODE": mode},
                text=True, capture_output=True, timeout=10,
            )
            return result, log.read_text()

    def test_reuses_annotated_title_after_reading_pinned_installer_version(self):
        result, calls = self.run_fixture()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Reusing open WinGet PR", result.stdout)
        self.assertIn("version 1.2.10", result.stdout)
        self.assertIn(f"?ref={HEAD}", calls)
        self.assertNotRegex(calls, r"git push|gh pr (edit|create)")

    def test_refuses_ambiguous_or_wrong_package_manifest(self):
        for mode in ("ambiguous", "wrong"):
            with self.subTest(mode=mode):
                result, calls = self.run_fixture(mode)
                self.assertEqual(result.returncode, 1)
                self.assertIn("unrecognized version", result.stderr)
                self.assertNotRegex(calls, r"git push|gh pr (edit|create)")
