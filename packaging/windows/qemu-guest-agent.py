#!/usr/bin/env python3
"""Small QEMU Guest Agent helper for Windows packaging verification.

This is intentionally host-side and dependency-free. It talks to the QGA Unix
socket that Quickemu exposes for a running Windows VM.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import json
import os
import pty
import select
import socket
import sys
import time
from pathlib import Path


def remaining_time(deadline: float) -> float:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("QGA deadline exhausted")
    return remaining


class DecodedReplyTimeout(TimeoutError):
    def __init__(self, reply: dict):
        super().__init__("QGA deadline exhausted after decoding a reply")
        self.reply = reply


def qga_call(socket_path: Path, payload: dict, timeout: float = 30, *,
             deadline: float | None = None) -> dict:
    call_deadline = time.monotonic() + timeout
    if deadline is not None:
        call_deadline = min(call_deadline, deadline)
    body = (json.dumps(payload) + "\n").encode("utf-8")
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
        client.settimeout(remaining_time(call_deadline))
        client.connect(str(socket_path))
        client.settimeout(remaining_time(call_deadline))
        client.sendall(body)
        chunks: list[bytes] = []
        while True:
            client.settimeout(remaining_time(call_deadline))
            try:
                chunk = client.recv(65536)
            except TimeoutError:
                remaining_time(call_deadline)
                break
            if not chunk:
                break
            chunks.append(chunk)
            if b"\n" in chunk:
                break
    raw = b"".join(chunks).decode("utf-8", errors="replace")
    if not raw:
        raise RuntimeError("empty QGA response")
    response = json.loads(raw)
    if not isinstance(response, dict):
        raise RuntimeError("invalid QGA response envelope")
    if "error" in response:
        error = response["error"]
        kind = error.get("class") if isinstance(error, dict) else None
        if not isinstance(kind, str) or not kind.isascii() or not kind.isidentifier() or len(kind) > 64:
            kind = "unknown class"
        raise RuntimeError(f"QGA error: {kind}")
    if "return" not in response:
        raise RuntimeError("QGA response has no return value")
    # Only the launch reply carries a pid that must survive a late decode.
    if payload.get("execute") == "guest-exec" and time.monotonic() >= call_deadline:
        raise DecodedReplyTimeout(response)
    return response


def guest_exec(socket_path: Path, args: list[str], timeout: int) -> int:
    deadline = time.monotonic() + timeout
    try:
        start = qga_call(
            socket_path,
            {
                "execute": "guest-exec",
                "arguments": {
                    "path": args[0],
                    "arg": args[1:],
                    "capture-output": True,
                },
            },
            timeout=min(30, remaining_time(deadline)),
            deadline=deadline,
        )
    except TimeoutError as error:
        if isinstance(error, DecodedReplyTimeout):
            returned = error.reply.get("return")
            pid = returned.get("pid") if isinstance(returned, dict) else None
            if type(pid) is int and pid > 0:
                raise TimeoutError(f"guest launch reply exceeded its deadline; guest pid {pid} may still be running") from error
        raise TimeoutError("guest launch timed out; launch outcome unknown") from error
    returned = start.get("return")
    pid = returned.get("pid") if isinstance(returned, dict) else None
    if type(pid) is not int or pid <= 0:
        raise RuntimeError("guest launch reply has no valid pid")
    timed_out = f"guest command timed out after {timeout}s; guest pid {pid} may still be running"
    while True:
        try:
            status_budget = min(30, remaining_time(deadline))
        except TimeoutError as error:
            raise TimeoutError(timed_out) from error
        try:
            status = qga_call(
                socket_path,
                {"execute": "guest-exec-status", "arguments": {"pid": pid}},
                timeout=status_budget,
                deadline=deadline,
            )["return"]
        except (OSError, RuntimeError, KeyError, ValueError) as error:
            if isinstance(error, TimeoutError) and time.monotonic() >= deadline:
                raise TimeoutError(timed_out) from error
            raise RuntimeError(f"guest status unavailable; guest pid {pid} may still be running") from error
        if not isinstance(status, dict):
            raise RuntimeError(f"invalid guest status reply; guest pid {pid} may still be running")
        if status.get("exited"):
            stdout = decode_output(status.get("out-data"))
            stderr = decode_output(status.get("err-data"))
            if stdout:
                print(stdout, end="" if stdout.endswith("\n") else "\n")
            if stderr:
                print(stderr, end="" if stderr.endswith("\n") else "\n", file=sys.stderr)
            signal = status.get("signal")
            if type(signal) is int:
                raise RuntimeError(f"guest process terminated by signal {signal}; guest pid {pid}")
            exit_code = status.get("exitcode")
            if type(exit_code) is not int:
                raise RuntimeError(f"guest process reported no numeric exit code; guest pid {pid}")
            return exit_code
        time_left = deadline - time.monotonic()
        if time_left <= 0:
            raise TimeoutError(timed_out)
        time.sleep(min(1, time_left))


def decode_output(value: str | None) -> str:
    if value is None or value == "":
        return ""
    if not isinstance(value, str):
        raise RuntimeError("guest output is not valid base64")
    try:
        return base64.b64decode(value, validate=True).decode("utf-8", errors="replace")
    except (binascii.Error, ValueError):
        raise RuntimeError("guest output is not valid base64") from None


def powershell(socket_path: Path, script: str, timeout: int) -> int:
    return guest_exec(
        socket_path,
        [
            "powershell.exe",
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-Command",
            script,
        ],
        timeout,
    )


def quickemu_share_fix(socket_path: Path, timeout: int) -> int:
    script = r"""
$ErrorActionPreference = 'Continue'
$paths = @(
  'HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters',
  'HKLM:\SOFTWARE\Policies\Microsoft\Windows\LanmanWorkstation'
)
foreach ($path in $paths) {
  New-Item -Path $path -Force | Out-Null
  New-ItemProperty -Path $path -Name AllowInsecureGuestAuth -PropertyType DWord -Value 1 -Force | Out-Null
  New-ItemProperty -Path $path -Name RequireSecuritySignature -PropertyType DWord -Value 0 -Force | Out-Null
  New-ItemProperty -Path $path -Name EnableSecuritySignature -PropertyType DWord -Value 0 -Force | Out-Null
}
Set-SmbClientConfiguration -RequireSecuritySignature $false -EnableSecuritySignature $false -Force
$svcPath = 'HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters'
New-ItemProperty -Path $svcPath -Name ServiceDll -PropertyType ExpandString -Value '%SystemRoot%\System32\wkssvc.dll' -Force | Out-Null
New-ItemProperty -Path $svcPath -Name ServiceDllUnloadOnStop -PropertyType DWord -Value 1 -Force | Out-Null
Start-Service LanmanWorkstation -ErrorAction SilentlyContinue
if ((Get-Service LanmanWorkstation).Status -ne 'Running') {
  Restart-Service LanmanWorkstation -Force
}
Start-Sleep -Seconds 2
Get-Service LanmanWorkstation | Format-List Name,Status,StartType
Get-SmbClientConfiguration | Format-List RequireSecuritySignature,EnableSecuritySignature,EnableInsecureGuestLogons
cmd /c net view \\10.0.2.4
cmd /c dir \\10.0.2.4\qemu
"""
    return powershell(socket_path, script, timeout)


def create_user(socket_path: Path, username: str, password: str, timeout: int) -> int:
    escaped_user = username.replace("'", "''")
    escaped_password = password.replace("'", "''")
    script = rf"""
$ErrorActionPreference = 'Stop'
$password = ConvertTo-SecureString '{escaped_password}' -AsPlainText -Force
if (-not (Get-LocalUser -Name '{escaped_user}' -ErrorAction SilentlyContinue)) {{
  New-LocalUser -Name '{escaped_user}' -Password $password -FullName 'Clawmeter VM Test' -Description 'Local account for Clawmeter VM verification' -PasswordNeverExpires
}}
net user '{escaped_user}' '{escaped_password}' /active:yes /passwordreq:yes /expires:never
if ($LASTEXITCODE -ne 0) {{ exit $LASTEXITCODE }}
Enable-LocalUser -Name '{escaped_user}'
Get-LocalUser -Name '{escaped_user}' | Format-List Name,Enabled,PasswordRequired,PasswordExpires,LastLogon
"""
    return powershell(socket_path, script, timeout)


def smoke_ssh(port: int, username: str, password: str, command: str) -> int:
    cmd = [
        "ssh",
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
        "-o",
        "PubkeyAuthentication=no",
        "-p",
        str(port),
        f"{username}@localhost",
        "powershell",
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-Command",
        command,
    ]
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp(cmd[0], cmd)

    buffer = b""
    password_sent = False
    deadline = time.time() + 60
    try:
        while time.time() < deadline:
            ready, _, _ = select.select([fd], [], [], 1)
            if not ready:
                continue
            try:
                chunk = os.read(fd, 4096)
            except OSError:
                break
            if not chunk:
                break
            sys.stdout.write(chunk.decode("utf-8", errors="replace"))
            sys.stdout.flush()
            buffer += chunk.lower()
            if not password_sent and b"password:" in buffer:
                os.write(fd, (password + "\n").encode("utf-8"))
                password_sent = True
                buffer = b""
        else:
            raise TimeoutError("ssh smoke test timed out")
    finally:
        try:
            os.close(fd)
        except OSError:
            pass
    _, status = os.waitpid(pid, 0)
    if os.WIFEXITED(status):
        return os.WEXITSTATUS(status)
    return 1


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--socket", required=True, type=Path, help="QEMU Guest Agent Unix socket")
    parser.add_argument("--timeout", type=int, default=120)
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("ping")
    sub.add_parser("info")
    sub.add_parser("fix-quickemu-share")

    ps = sub.add_parser("powershell")
    ps.add_argument("script")

    user = sub.add_parser("create-test-user")
    user.add_argument("--username", default="ClawmeterTest")
    user.add_argument("--password", default="quickemu")

    ssh = sub.add_parser("smoke-ssh")
    ssh.add_argument("--port", type=int, required=True)
    ssh.add_argument("--username", default="ClawmeterTest")
    ssh.add_argument("--password", default="quickemu")
    ssh.add_argument(
        "--command",
        dest="ssh_command",
        default="whoami",
        help="PowerShell command to run as the test user",
    )

    args = parser.parse_args()
    if args.command == "ping":
        print(json.dumps(qga_call(args.socket, {"execute": "guest-ping"}), indent=2))
        return 0
    if args.command == "info":
        print(json.dumps(qga_call(args.socket, {"execute": "guest-info"}), indent=2))
        return 0
    if args.command == "powershell":
        return powershell(args.socket, args.script, args.timeout)
    if args.command == "fix-quickemu-share":
        return quickemu_share_fix(args.socket, args.timeout)
    if args.command == "create-test-user":
        return create_user(args.socket, args.username, args.password, args.timeout)
    if args.command == "smoke-ssh":
        return smoke_ssh(args.port, args.username, args.password, args.ssh_command)
    raise AssertionError(args.command)


if __name__ == "__main__":
    raise SystemExit(main())
