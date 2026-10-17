"""Source-contract checks; supplement with actionlint and live check-name evidence."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
class VerificationGraphTest(unittest.TestCase):
    def test_one_main_entrypoint_and_same_sha_dependencies(self):
        for name in ("ci.yml", "platform-verify.yml"):
            text = (ROOT / ".github/workflows" / name).read_text()
            header = text.split("\npermissions:", 1)[0]
            self.assertIn("  workflow_call:", header)
            self.assertIn("  pull_request:", header)
            self.assertNotIn("  push:", header)
            self.assertIn("ref: ${{ github.sha }}", text)
        release = (ROOT / ".github/workflows/semantic-release.yml").read_text()
        self.assertIn("  push:\n    branches:\n      - main", release)
        self.assertEqual(release.count("uses: ./.github/workflows/ci.yml"), 1)
        self.assertEqual(release.count("uses: ./.github/workflows/platform-verify.yml"), 1)
        self.assertIn("needs: [test, platform-verify]", release)
        self.assertIn("ref: ${{ github.sha }}", release)
        gate = release.split("\n  release:", 1)[1].split("\n  build:", 1)[0]
        self.assertNotIn("if: always()", gate)
        self.assertNotIn("continue-on-error:", gate)

if __name__ == "__main__":
    unittest.main()
