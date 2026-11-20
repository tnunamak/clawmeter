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
        with patch.object(agent, 'qga_call', return_value={'return': {'pid': 42}}), patch.object(agent.time, 'monotonic', side_effect=[0, 10]):
            with self.assertRaisesRegex(TimeoutError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 1)

    def test_poll_failure_exposes_unsettled_guest_pid(self):
        with patch.object(agent, 'qga_call', side_effect=[{'return': {'pid': 42}}, OSError('status unavailable')]):
            with self.assertRaisesRegex(RuntimeError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 10)


    def test_guest_deadline_covers_launch_io_polling_and_sleep(self):
        now = 0.0
        sent, limits, sleeps = [], [], []
        replies = [
            (0.1, 0.1, [(0.05, b'{"return":'), (0.05, b'{"pid":42}}\n')]),
            (0.1, 0.1, [(0.1, b'{"return":{"exited":false}}\n')]),
        ]

        def sleep(delay):
            nonlocal now
            sleeps.append(delay)
            now += delay

        class FakeSocket:
            def __init__(self, *args):
                self.connect_time, self.send_time, self.chunks = replies.pop(0)
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def settimeout(self, seconds): self.limit = seconds
            def spend(self, phase, amount):
                nonlocal now
                limits.append((phase, self.limit, now))
                if amount > self.limit:
                    now += self.limit
                    raise TimeoutError('synthetic timeout')
                now += amount
            def connect(self, address): self.spend('connect', self.connect_time)
            def sendall(self, body):
                self.spend('send', self.send_time)
                sent.append(agent.json.loads(body)['execute'])
            def recv(self, _size):
                seconds, payload = self.chunks.pop(0)
                self.spend('recv', seconds)
                return payload

        with patch.object(agent.time, 'monotonic', lambda: now), \
                patch.object(agent.time, 'time', lambda: now), \
                patch.object(agent.time, 'sleep', sleep), \
                patch.object(agent.socket, 'socket', FakeSocket):
            with self.assertRaisesRegex(TimeoutError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 1)
        self.assertEqual(sent, ['guest-exec', 'guest-exec-status'])
        self.assertEqual(len(limits), 7)
        for phase, seconds, started in limits:
            with self.subTest(phase=phase, started=started):
                self.assertAlmostEqual(seconds, 1 - started)
        self.assertEqual(len(sleeps), 1)
        self.assertAlmostEqual(sleeps[0], 0.4)
        self.assertAlmostEqual(now, 1)

    def test_guest_launch_timeout_has_unknown_outcome_without_resubmission(self):
        with patch.object(agent, 'qga_call', side_effect=TimeoutError('synthetic')) as call:
            with self.assertRaisesRegex(TimeoutError, 'launch outcome unknown'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 1)
            self.assertEqual(call.call_count, 1)

if __name__ == '__main__': unittest.main()
