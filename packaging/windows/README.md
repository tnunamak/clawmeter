# Windows Packaging

## Ideal End State

Clawmeter has one Windows desktop product shape:

- `ClawmeterSetup.exe` is the canonical installer for direct downloads.
- `winget install --id tnunamak.Clawmeter -e` installs that same setup exe.
- `clawmeter-windows-amd64.exe` remains available only as a portable/advanced artifact.

The setup installer is per-user by default. It installs into `%LOCALAPPDATA%\Programs\Clawmeter`, creates a Start Menu shortcut that launches `clawmeter tray`, adds `clawmeter` to the user `PATH`, registers a normal uninstall entry, offers launch-at-login as an opt-in task, and launches the tray after interactive installs. Silent installs do not launch the tray.

## Release Flow

1. CI builds `clawmeter-windows-amd64.exe`.
2. CI compiles `ClawmeterSetup.exe` with Inno Setup.
3. Release assets include both files plus `SHA256SUMS.txt`.
4. `packaging/winget/generate.sh vX.Y.Z` generates a WinGet manifest that points at `ClawmeterSetup.exe`.
5. The manifest is validated and installed in Windows with local manifest mode.
6. The generated manifest directory is submitted to `microsoft/winget-pkgs`.

## Verification

Local Windows VM smoke:

```powershell
.\packaging\windows\build-inno.ps1 -BinaryPath C:\temp\clawmeter.exe -Version 0.0.0-test -OutputDir C:\temp
C:\temp\ClawmeterSetup.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /TASKS=addtopath
clawmeter providers
winget settings --enable LocalManifestFiles
winget validate .\packaging\winget\out\manifests\t\tnunamak\Clawmeter\<version>
winget install --manifest .\packaging\winget\out\manifests\t\tnunamak\Clawmeter\<version> --silent --accept-source-agreements --accept-package-agreements
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

Manual interactive smoke:

- Run `ClawmeterSetup.exe`.
- Confirm the installer shows a normal Windows wizard.
- Confirm Start Menu has `Clawmeter`.
- Launch `Clawmeter` and confirm the tray appears.
- Open a new PowerShell and confirm `clawmeter providers` works.
- Uninstall and confirm the install directory, Start Menu shortcut, startup registry value, and PATH entry are removed.

## Known Non-Goals

Code signing and SmartScreen reputation are separate tracks. The installer gives the right Windows UX, but an unsigned installer can still produce trust prompts until signing/reputation is handled.
