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

    def test_guest_agent_error_names_class_without_raw_description(self):
        with patch.object(agent.socket, 'socket') as transport:
            client = transport.return_value.__enter__.return_value
            client.recv.return_value = b'{"error":{"class":"GenericError","desc":"private fixture"}}\n'
            with self.assertRaisesRegex(RuntimeError, 'GenericError') as caught:
                agent.qga_call(Path('fixture.sock'), {'execute': 'guest-ping'})
            self.assertNotIn('private fixture', str(caught.exception))

    def test_signal_termination_does_not_return_zero(self):
        replies = [{'return': {'pid': 42}}, {'return': {'exited': True, 'signal': 9}}]
        with patch.object(agent, 'qga_call', side_effect=replies):
            with self.assertRaisesRegex(RuntimeError, 'signal 9'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 10)

    def test_timeout_does_not_expose_guest_arguments(self):
        with patch.object(agent, 'qga_call', return_value={'return': {'pid': 42}}), patch.object(agent.time, 'monotonic', side_effect=[0, 0, 10]):
            with self.assertRaisesRegex(TimeoutError, 'after 1s; guest pid 42') as caught:
                agent.guest_exec(Path('unused'), ['fixture.exe', 'synthetic-private-argument'], 1)
        self.assertNotIn('fixture.exe', str(caught.exception))
        self.assertNotIn('synthetic-private-argument', str(caught.exception))

    def test_reply_timeout_without_bytes_reports_empty_response(self):
        with patch.object(agent.socket, 'socket') as transport, patch.object(agent.time, 'monotonic', side_effect=[0, 0, 1, 2, 2]):
            client = transport.return_value.__enter__.return_value
            client.recv.side_effect = TimeoutError('fixture deadline')
            with self.assertRaisesRegex(RuntimeError, '^empty QGA response$'):
                agent.qga_call(Path('fixture.sock'), {'execute': 'guest-ping'}, timeout=5)
            self.assertEqual([call.args[0] for call in client.settimeout.call_args_list], [5, 4, 3])
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
        with patch.object(agent, 'qga_call', return_value={'return': {'pid': 42}}), patch.object(agent.time, 'monotonic', side_effect=[0, 0, 10]):
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


    def test_guest_result_preserves_exit_status_and_separate_output(self):
        from io import StringIO

        replies = [
            {'return': {'pid': 42}},
            {'return': {'exited': True, 'exitcode': 9, 'out-data': 'b3V0Cg==', 'err-data': 'ZXJy'}},
        ]
        with (
            patch.object(agent, 'qga_call', side_effect=replies),
            patch.object(agent.sys, 'stdout', new_callable=StringIO) as stdout,
            patch.object(agent.sys, 'stderr', new_callable=StringIO) as stderr,
        ):
            self.assertEqual(agent.guest_exec(Path('unused'), ['fixture.exe'], 10), 9)
        self.assertEqual(stdout.getvalue(), 'out\n')
        self.assertEqual(stderr.getvalue(), 'err\n')

    def test_guest_running_status_does_not_publish_partial_output(self):
        from io import StringIO

        replies = [
            {'return': {'pid': 42}},
            {'return': {'exited': False, 'out-data': 'cGFydGlhbA==', 'err-data': 'cGFydGlhbA=='}},
            {'return': {'exited': True, 'exitcode': 0, 'out-data': 'ZmluYWw='}},
        ]
        with (
            patch.object(agent, 'qga_call', side_effect=replies),
            patch.object(agent.time, 'sleep'),
            patch.object(agent.sys, 'stdout', new_callable=StringIO) as stdout,
            patch.object(agent.sys, 'stderr', new_callable=StringIO) as stderr,
        ):
            self.assertEqual(agent.guest_exec(Path('unused'), ['fixture.exe'], 10), 0)
        self.assertEqual(stdout.getvalue(), 'final\n')
        self.assertEqual(stderr.getvalue(), '')

    def test_timeout_does_not_include_guest_command_arguments(self):
        secret = 'synthetic-guest-password'
        with patch.object(agent, 'qga_call', return_value={'return': {'pid': 42}}), patch.object(agent.time, 'time', side_effect=[0, 10]):
            with self.assertRaises(TimeoutError) as caught:
                agent.guest_exec(Path('unused'), ['fixture.exe', secret], 1)
        self.assertIn('guest pid 42 may still be running', str(caught.exception))
        self.assertNotIn(secret, str(caught.exception))

    def test_guest_deadline_includes_launch_polling_and_sleep(self):
        now = 0.0
        calls, sleeps = [], []

        def call(_socket, payload, timeout=30, **_kwargs):
            nonlocal now
            calls.append((payload['execute'], timeout))
            if payload['execute'] == 'guest-exec':
                now += 2
                return {'return': {'pid': 42}}
            if len(calls) > 2:
                return {'return': {'exited': True, 'exitcode': 0}}
            now += .75
            return {'return': {'exited': False}}

        def sleep(delay):
            nonlocal now
            sleeps.append(delay)
            now += delay

        with (
            patch.object(agent, 'qga_call', side_effect=call),
            patch.object(agent.time, 'monotonic', lambda: now),
            patch.object(agent.time, 'sleep', side_effect=sleep),
        ):
            with self.assertRaisesRegex(TimeoutError, 'guest pid 42 may still be running'):
                agent.guest_exec(Path('unused'), ['fixture.exe'], 3)
        self.assertEqual(calls, [('guest-exec', 3), ('guest-exec-status', 1)])
        self.assertEqual(sleeps, [.25])
        self.assertEqual(now, 3)

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

    def test_decoded_launch_reply_keeps_pid_when_parse_crosses_deadline(self):
        now = 0.0
        sent = []
        loads = agent.json.loads

        class FakeSocket:
            def __init__(self, *args): pass
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def settimeout(self, seconds): pass
            def connect(self, address): pass
            def sendall(self, body): sent.append(loads(body)['execute'])
            def recv(self, size): return b'{"return":{"pid":42}}\n'

        def parse_past_deadline(raw):
            nonlocal now
            reply = loads(raw)
            now = 1.01
            return reply

        with patch.object(agent.socket, 'socket', FakeSocket), \
                patch.object(agent.time, 'monotonic', lambda: now), \
                patch.object(agent.json, 'loads', parse_past_deadline):
            with self.assertRaisesRegex(TimeoutError, 'guest pid 42 may still be running') as error:
                agent.guest_exec(Path('unused'), ['fixture.exe', 'private-fixture-argument'], 1)
        self.assertEqual(sent, ['guest-exec'])
        self.assertNotIn('private-fixture-argument', str(error.exception))

if __name__ == '__main__': unittest.main()
