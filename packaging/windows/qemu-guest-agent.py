#!/usr/bin/env python3
"""Small QEMU Guest Agent helper for Windows packaging verification.

This is intentionally host-side and dependency-free. It talks to the QGA Unix
socket that Quickemu exposes for a running Windows VM.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import pty
import select
import socket
import sys
import time
from pathlib import Path


def qga_call(socket_path: Path, payload: dict, timeout: int = 30) -> dict:
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
        client.settimeout(timeout)
        client.connect(str(socket_path))
        client.sendall((json.dumps(payload) + "\n").encode("utf-8"))
        chunks: list[bytes] = []
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                chunk = client.recv(65536)
            except TimeoutError:
                break
            if not chunk:
                break
            chunks.append(chunk)
            if b"\n" in chunk:
                break
    raw = b"".join(chunks).decode("utf-8", errors="replace")
    if not raw:
        raise RuntimeError("empty QGA response")
    return json.loads(raw)


def guest_exec(socket_path: Path, args: list[str], timeout: int) -> int:
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
    )
    pid = start["return"]["pid"]
    deadline = time.time() + timeout
    while time.time() < deadline:
        status = qga_call(
            socket_path,
            {"execute": "guest-exec-status", "arguments": {"pid": pid}},
        )["return"]
        if status.get("exited"):
            stdout = decode_output(status.get("out-data"))
            stderr = decode_output(status.get("err-data"))
            if stdout:
                print(stdout, end="" if stdout.endswith("\n") else "\n")
            if stderr:
                print(stderr, end="" if stderr.endswith("\n") else "\n", file=sys.stderr)
            return int(status.get("exitcode", 0))
        time.sleep(1)
    raise TimeoutError(f"guest command timed out after {timeout}s: {args}")


def decode_output(value: str | None) -> str:
    if not value:
        return ""
    return base64.b64decode(value).decode("utf-8", errors="replace")


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
$ErrorActionPreference = 'Continue'
$password = ConvertTo-SecureString '{escaped_password}' -AsPlainText -Force
if (-not (Get-LocalUser -Name '{escaped_user}' -ErrorAction SilentlyContinue)) {{
  New-LocalUser -Name '{escaped_user}' -Password $password -FullName 'Clawmeter VM Test' -Description 'Local account for Clawmeter VM verification' -PasswordNeverExpires
}}
net user '{escaped_user}' '{escaped_password}' /active:yes /passwordreq:yes /expires:never
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
