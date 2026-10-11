import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('guest_agent', Path(__file__).with_name('qemu-guest-agent.py'))
agent = importlib.util.module_from_spec(spec)
spec.loader.exec_module(agent)

class GuestAgentTest(unittest.TestCase):
    def test_password_rejection_returns_native_failure(self):
        def execute(_, script, timeout):
            mocks = """
function Get-LocalUser { [pscustomobject]@{Name='fixture'; Enabled=$true} }
function Enable-LocalUser { 'enabled' }
function net { $global:LASTEXITCODE = 5 }
"""
            with tempfile.TemporaryDirectory() as d:
                file = Path(d) / 'fixture.ps1'
                file.write_text(mocks + script)
                return subprocess.run(['pwsh', '-NoProfile', '-File', str(file)], capture_output=True, timeout=15).returncode
        with patch.object(agent, 'powershell', execute):
            self.assertEqual(agent.create_user(Path('unused'), 'fixture', 'fake-value', 5), 5)

    def test_timeout_exposes_unsettled_guest_pid(self):
        with patch.object(agent, 'qga_call', return_value={'return': {'pid': 42}}), patch.object(agent.time, 'time', side_effect=[0, 10]):
            with self.assertRaisesRegex(TimeoutError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 1)

    def test_poll_failure_exposes_unsettled_guest_pid(self):
        with patch.object(agent, 'qga_call', side_effect=[{'return': {'pid': 42}}, OSError('status unavailable')]):
            with self.assertRaisesRegex(RuntimeError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 10)

if __name__ == '__main__': unittest.main()
