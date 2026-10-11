from pathlib import Path
import re
import unittest

class DocumentedOverridesTest(unittest.TestCase):
    def test_documented_environment_overrides_exist_in_submission_script(self):
        root = Path(__file__).parent
        text = (root/'README.md').read_text()
        script = (root/'submit-pr.sh').read_text()
        for variable in set(re.findall(r'\b(WINGET_[A-Z_]+)=', text)):
            self.assertIn(variable, script, f'unsupported documented override: {variable}')

if __name__ == '__main__': unittest.main()
