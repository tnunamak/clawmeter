import json
import os
from pathlib import Path
import runpy
import stat
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

SOURCE = Path(__file__).with_name("package-extension.py")
MEMBERS = ["background.js", "console.js", "resets.js", "popup.html", "popup.js", "icon.png"]


class PackageExtensionTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.extension = self.root / "extension"
        self.extension.mkdir()
        self.script = self.root / "scripts" / SOURCE.name
        self.script.parent.mkdir()
        self.script.write_bytes(SOURCE.read_bytes())
        self.output = self.root / "clawmeter-extension-1.2.3.zip"
        self.extension.joinpath("manifest.json").write_text(
            json.dumps({"version": "1.2.3", "icons": {"16": "icon.png"}})
        )
        for name in MEMBERS:
            self.extension.joinpath(name).write_bytes(b"fixture contents " * 32)

    def package(self):
        with patch.object(sys, "argv", [str(self.script), str(self.output)]):
            runpy.run_path(str(self.script), run_name="__main__")

    def test_packaged_zip_gets_the_umask_default_mode_not_the_temp_file_mode(self):
        old = os.umask(0o022)
        try:
            self.package()
        finally:
            os.umask(old)
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o644)

    def test_failed_rebuild_keeps_last_good_zip(self):
        with zipfile.ZipFile(self.output, "w") as original:
            original.writestr("manifest.json", '{"version":"1.2.3"}')
        before = self.output.read_bytes()
        real_read = Path.read_bytes

        def fail_on_member(path):
            if path == self.extension / "popup.js":
                raise OSError("fixture read failure")
            return real_read(path)

        with patch.object(Path, "read_bytes", fail_on_member):
            with self.assertRaisesRegex(OSError, "fixture read failure"):
                self.package()
        self.assertEqual(self.output.read_bytes(), before)
        self.assertEqual(sorted(item.name for item in self.root.iterdir()),
                         [self.output.name, "extension", "scripts"])

    def test_same_version_zip_entries_are_deflated_with_deterministic_dates(self):
        self.package()
        before = self.output.read_bytes()
        with zipfile.ZipFile(self.output) as archive:
            entries = archive.infolist()
            self.assertEqual([item.filename for item in entries], ["manifest.json", *MEMBERS])
            self.assertEqual([item.date_time for item in entries], [(2026, 1, 1, 0, 0, 0)] * len(entries))
            self.assertEqual([item.compress_type for item in entries], [zipfile.ZIP_DEFLATED] * len(entries))
        self.package()
        self.assertEqual(self.output.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
