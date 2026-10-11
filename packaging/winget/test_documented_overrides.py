from pathlib import Path
import re
import unittest


class DocumentedOverridesTest(unittest.TestCase):
    def test_documented_environment_overrides_exist_in_packaging_scripts(self):
        root = Path(__file__).parent
        repository = root.parent.parent
        readme = (root / "README.md").read_text()
        source_files = list(root.glob("*.sh")) + list((repository / ".github" / "workflows").glob("*.yml"))
        source = "\n".join(path.read_text() for path in source_files)
        supported = set(re.findall(r"\bWINGET_[A-Z_]+\b", source))

        for variable in set(re.findall(r"\bWINGET_[A-Z_]+\b", readme)):
            self.assertIn(variable, supported, f"unsupported documented override: {variable}")


if __name__ == "__main__":
    unittest.main()
