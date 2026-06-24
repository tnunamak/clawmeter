# Windows Packaging

Windows has one polished desktop product shape: `ClawmeterSetup.exe`.

Priority order:

1. Direct installer: download `ClawmeterSetup.exe` from the GitHub release.
2. WinGet: `winget install --id tnunamak.Clawmeter -e`, once the package is accepted.
3. Portable fallback: `clawmeter-windows-amd64.exe` for advanced/manual use.
4. Signing: next track, after installer and WinGet verification are stable.

The setup installer is per-user by default. It installs into `%LOCALAPPDATA%\Programs\Clawmeter`, creates a Start Menu shortcut that launches `clawmeter tray`, adds `clawmeter` to the user `PATH`, registers an uninstall entry, offers launch-at-login as an opt-in task, and launches the tray after interactive installs. Silent installs do not launch the tray.

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

For signed builds:

```powershell
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -IncludeStartup -ExpectSigned
```

The full release plan and verification matrix live in [PLAN.md](PLAN.md).

## Non-Goals

`install.ps1` is not the primary Windows onboarding path. It remains an advanced fallback for local/portable scripting.

Code signing and SmartScreen reputation are related but separate. Signing proves publisher identity and artifact integrity; SmartScreen reputation must be earned and checked from a clean Windows environment using the public release URL.
