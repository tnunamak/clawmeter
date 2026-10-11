from pathlib import Path
import re
import unittest

class DocumentedOverridesTest(unittest.TestCase):
    def test_documented_environment_overrides_exist_in_packaging_scripts(self):
        root = Path(__file__).parent
        text = (root/'README.md').read_text()
        scripts = '\n'.join((root/name).read_text() for name in ('submit-pr.sh', 'generate.sh'))
        for variable in set(re.findall(r'\b(WINGET_[A-Z_]+)=', text)):
            self.assertIn(variable, scripts, f'unsupported documented override: {variable}')

if __name__ == '__main__': unittest.main()
