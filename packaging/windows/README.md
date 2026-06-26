# Windows Packaging

Windows has one polished desktop product shape: `ClawmeterSetup.exe`.

Priority order:

1. Direct installer: download `ClawmeterSetup.exe` from the GitHub release.
2. WinGet: `winget install --id tnunamak.Clawmeter -e`, once the package is accepted.
3. Portable fallback: `clawmeter-windows-amd64.exe` for advanced/manual use.
4. Signing: later track, after the project has more public reputation.

The setup installer is per-user by default. It installs into `%LOCALAPPDATA%\Programs\Clawmeter`, creates a Start Menu shortcut that launches `clawmeter tray`, adds `clawmeter` to the user `PATH`, registers an uninstall entry, offers launch-at-login as an opt-in task, offers automatic update checks as an opt-out task, and launches the tray after interactive installs. Silent installs do not launch the tray.

## Build

```powershell
.\packaging\windows\build-inno.ps1 -BinaryPath C:\temp\clawmeter.exe -Version 0.0.0-test -OutputDir C:\temp
```

Optional signing support is built in but disabled unless the caller supplies a sign tool:

```powershell
.\packaging\windows\build-inno.ps1 `
  -BinaryPath C:\temp\clawmeter-windows-amd64.exe `
  -Version vX.Y.Z `
  -OutputDir C:\temp `
  -SignToolName clawmeterSignTool `
  -SignToolCommand 'signtool.exe sign /a $p'
```

Certificate paths and passwords must come from the local environment or CI secrets, not the repo.
The sign tool command should accept Inno's `$p` parameter placeholder; the script supplies the description, timestamp, and file path through `-SignToolParameters`.

## Verify

CI and manual VM runs should use the shared verifier:

```powershell
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -IncludeStartup
```

Verify the update-check opt-out path too:

```powershell
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -DisableUpdates
```

For signed builds:

```powershell
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -IncludeStartup -ExpectSigned
```

To verify the public release artifact in a clean Windows VM:

```powershell
.\packaging\windows\verify-release.ps1 -Version vX.Y.Z -IncludeStartup
.\packaging\windows\verify-release.ps1 -Version vX.Y.Z -DisableUpdates -ScanWithDefender
```

`verify-release.ps1` downloads `ClawmeterSetup.exe`, checks it against `SHA256SUMS.txt`, optionally scans it with Microsoft Defender, and then delegates install/uninstall checks to `verify-installer.ps1`.

If Windows Security flags Clawmeter, collect a local evidence bundle before changing anything:

```powershell
.\packaging\windows\collect-defender-evidence.ps1 -Path .\ClawmeterSetup.exe -Scan
```

The script writes hashes, Authenticode status, Defender status, and matching detections to a local folder. It does not submit anything automatically. Submit confirmed false positives through Microsoft's file submission portal: <https://www.microsoft.com/en-us/wdsi/filesubmission>.

## Quickemu VM Control

For local Quickemu-based Windows verification, prefer QEMU Guest Agent for privileged setup and SSH for normal-user smoke tests.

Guest Agent smoke test:

```bash
python3 packaging/windows/qemu-guest-agent.py \
  --socket <agent.sock> \
  ping
```

Repair Quickemu's `\\10.0.2.4\qemu` share inside Windows if `dir \\10.0.2.4\qemu` fails even though port 445 is reachable:

```bash
python3 packaging/windows/qemu-guest-agent.py \
  --socket <agent.sock> \
  fix-quickemu-share
```

The repair restores the `LanmanWorkstation` service DLL registration and disables SMB signing / guest-auth policies for this local VM share. Do not use that policy change as general Windows setup advice.

Create a local test account and verify normal-user command execution over Quickemu's forwarded SSH port:

```bash
python3 packaging/windows/qemu-guest-agent.py \
  --socket <agent.sock> \
  create-test-user

python3 packaging/windows/qemu-guest-agent.py \
  --socket <agent.sock> \
  smoke-ssh --port 22220
```

The default test account is `ClawmeterTest` with password `quickemu`; it is for disposable local VM verification only.
Do not use SSH logon sessions as the oracle for `\\10.0.2.4\qemu` share access; Windows applies different SMB behavior to SSH network logons. Use `fix-quickemu-share` for privileged share verification, then run installer checks from the desktop or copy artifacts through Guest Agent when needed.

The full release plan and verification matrix live in [PLAN.md](PLAN.md).

## Non-Goals

`install.ps1` is not the primary Windows onboarding path. It remains an advanced fallback for local/portable scripting.

Code signing and SmartScreen reputation are related but separate. Signing proves publisher identity and artifact integrity; SmartScreen reputation must be earned and checked from a clean Windows environment using the public release URL.
