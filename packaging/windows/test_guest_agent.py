import importlib.util
import json
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

    def test_reply_timeout_without_bytes_reports_empty_response(self):
        with patch.object(agent.socket, 'socket') as transport:
            client = transport.return_value.__enter__.return_value
            client.recv.side_effect = TimeoutError('fixture deadline')
            with self.assertRaisesRegex(RuntimeError, '^empty QGA response$'):
                agent.qga_call(Path('fixture.sock'), {'execute': 'guest-ping'}, timeout=5)
            client.settimeout.assert_called_once_with(5)
            client.recv.assert_called_once()
            transport.return_value.__exit__.assert_called_once()

    def test_peer_close_mid_reply_reports_json_error_and_closes_transport(self):
        with patch.object(agent.socket, 'socket') as transport:
            client = transport.return_value.__enter__.return_value
            client.recv.side_effect = [b'{"return":', b'']
            with self.assertRaises(json.JSONDecodeError):
                agent.qga_call(Path('fixture.sock'), {'execute': 'guest-ping'})
            self.assertEqual(client.recv.call_count, 2)
            transport.return_value.__exit__.assert_called_once()

    def test_timeout_exposes_unsettled_guest_pid(self):
        with patch.object(agent, 'qga_call', return_value={'return': {'pid': 42}}), patch.object(agent.time, 'time', side_effect=[0, 10]):
            with self.assertRaisesRegex(TimeoutError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 1)

    def test_poll_failure_exposes_unsettled_guest_pid(self):
        with patch.object(agent, 'qga_call', side_effect=[{'return': {'pid': 42}}, OSError('status unavailable')]):
            with self.assertRaisesRegex(RuntimeError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 10)


    def test_guest_failure_without_output_keeps_nonzero_exit_code(self):
        replies = [{'return': {'pid': 42}}, {'return': {'exited': True, 'exitcode': 7}}]
        with patch.object(agent, 'qga_call', side_effect=replies):
            self.assertEqual(agent.guest_exec(Path('unused'), ['fixture.exe'], 10), 7)

    def test_guest_failure_with_non_utf8_stderr_keeps_exit_code_and_visible_output(self):
        replies = [
            {'return': {'pid': 42}},
            {'return': {'exited': True, 'exitcode': 8, 'err-data': '/w=='}},
        ]
        with patch.object(agent, 'qga_call', side_effect=replies), patch.object(agent.sys, 'stderr') as stderr:
            self.assertEqual(agent.guest_exec(Path('unused'), ['fixture.exe'], 10), 8)
        self.assertEqual(''.join(call.args[0] for call in stderr.write.call_args_list), '\ufffd\n')

if __name__ == '__main__': unittest.main()
