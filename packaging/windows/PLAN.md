# Windows Distribution Plan

This is the release path for Windows:

1. Direct installer first: `ClawmeterSetup.exe` from the GitHub release.
2. WinGet second: `winget install --id tnunamak.Clawmeter -e`, installing the same setup exe after Microsoft accepts the package.
3. Portable fallback: `clawmeter-windows-amd64.exe` for advanced/manual use.
4. Signing next: Authenticode-sign the portable exe, setup exe, and uninstaller; then build SmartScreen reputation.

## Product Contract

`ClawmeterSetup.exe` is the normal Windows product. It must:

- install per-user into `%LOCALAPPDATA%\Programs\Clawmeter`;
- create a Start Menu shortcut that launches `clawmeter tray`;
- put `clawmeter` on the user `PATH`;
- register a normal uninstall entry;
- offer launch-at-login as an opt-in installer task;
- launch the tray after interactive installs only;
- avoid terminal flashes from tray menu actions;
- uninstall cleanly, including shortcut, Run key, and PATH cleanup.

WinGet is a distribution channel for that same installer, not a separate product shape. The portable exe is intentionally less polished and must remain documented as fallback/advanced use.

## Release Gates

### 1. Direct Installer

Run on every PR/main build:

```powershell
go build -tags tray -o C:\temp\clawmeter.exe .\cmd\clawmeter
choco install innosetup --no-progress -y
.\packaging\windows\build-inno.ps1 -BinaryPath C:\temp\clawmeter.exe -Version 0.0.0-test -OutputDir C:\temp
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -IncludeStartup
```

Before publishing a release, verify the final release asset in a clean Windows VM:

```powershell
$version = "0.22.0"
$base = "https://github.com/tnunamak/clawmeter/releases/download/v$version"
Invoke-WebRequest "$base/ClawmeterSetup.exe" -OutFile ClawmeterSetup.exe
Invoke-WebRequest "$base/SHA256SUMS.txt" -OutFile SHA256SUMS.txt
Get-FileHash .\ClawmeterSetup.exe -Algorithm SHA256
Select-String -Path .\SHA256SUMS.txt -Pattern "ClawmeterSetup.exe"
.\ClawmeterSetup.exe
```

Interactive VM checklist:

- The installer wizard opens without PowerShell/download-command ceremony.
- The publisher/trust prompt is understood for current unsigned builds.
- The default install creates `%LOCALAPPDATA%\Programs\Clawmeter\clawmeter.exe`.
- The Start Menu shortcut launches the tray.
- A new PowerShell can run `clawmeter providers`.
- Bare `clawmeter` produces useful output when piped: `clawmeter | Tee-Object clawmeter.log`.
- `Refresh Now` does not visibly flash a terminal window.
- Launch-at-login can be enabled and disabled from the tray.
- Uninstall removes the install dir executable, Start Menu shortcut, startup Run key, and PATH entry.

### 2. WinGet

Local rehearsal before or immediately after release upload:

```bash
WINGET_ASSET_PATH=/path/to/ClawmeterSetup.exe packaging/winget/generate.sh vX.Y.Z
WINGET_DRY_RUN=1 packaging/winget/submit-pr.sh vX.Y.Z
```

On Windows with local manifest mode:

```powershell
$version = "0.22.0"
$manifest = ".\packaging\winget\out\manifests\t\tnunamak\Clawmeter\$version"
winget validate --manifest $manifest --disable-interactivity
winget settings --enable LocalManifestFiles
winget install --manifest $manifest --silent --accept-source-agreements --accept-package-agreements
winget list --id tnunamak.Clawmeter -e
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

Before opening/updating the WinGet PR, CI must verify that the public release URL bytes match the local installer used to generate the manifest hash:

```bash
tag="vX.Y.Z"
curl -fsSL "https://github.com/tnunamak/clawmeter/releases/download/${tag}/ClawmeterSetup.exe" -o /tmp/ClawmeterSetup.exe
test "$(sha256sum artifacts/ClawmeterSetup.exe | cut -d' ' -f1)" = "$(sha256sum /tmp/ClawmeterSetup.exe | cut -d' ' -f1)"
```

After the Microsoft PR merges and the source refreshes:

```powershell
winget source update
winget show --id tnunamak.Clawmeter -e --source winget
winget install --id tnunamak.Clawmeter -e --source winget --silent
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

Expected PR behavior:

- First accepted submission is `New package`.
- Later releases become `New version`.
- Red closed PRs in `microsoft/winget-pkgs` are rejected/superseded/abandoned; accepted PRs merge purple.
- `wingetbot` validation runs in Azure DevOps, not GitHub Actions, so GitHub may show little beyond CLA status.

### 3. Portable Fallback

The portable exe remains useful for locked-down machines and debugging. It is not the recommended Windows onboarding path.

```powershell
Invoke-WebRequest "$base/clawmeter-windows-amd64.exe" -OutFile clawmeter.exe
Get-FileHash .\clawmeter.exe -Algorithm SHA256
.\clawmeter.exe providers
.\clawmeter.exe --json
```

Portable verification should prove the exe runs and reads existing credentials, but it does not need Start Menu, PATH, launch-at-login, or uninstall behavior.

### 4. Signing

Signing is next after the installer/WinGet path is stable.

Repo support:

- `packaging/windows/clawmeter.iss` accepts `AppSignTool`.
- `packaging/windows/build-inno.ps1` accepts `-SignToolName`, `-SignToolCommand`, and `-SignToolParameters`.
- When signing is enabled, Inno signs the setup exe, the uninstaller, and the bundled `clawmeter.exe`.

Example local shape, with certificate paths supplied outside the repo:

```powershell
.\packaging\windows\build-inno.ps1 `
  -BinaryPath C:\temp\clawmeter-windows-amd64.exe `
  -Version vX.Y.Z `
  -OutputDir C:\temp `
  -SignToolName clawmeterSignTool `
  -SignToolCommand 'signtool.exe sign /a $p'
```

Signing verification:

```powershell
Get-AuthenticodeSignature .\clawmeter-windows-amd64.exe | Format-List *
Get-AuthenticodeSignature .\ClawmeterSetup.exe | Format-List *
signtool verify /pa /tw /v .\clawmeter-windows-amd64.exe
signtool verify /pa /tw /v .\ClawmeterSetup.exe
.\packaging\windows\verify-installer.ps1 -InstallerPath .\ClawmeterSetup.exe -IncludeStartup -ExpectSigned
```

Checksums and WinGet manifest generation must happen after signing, because signing mutates artifact bytes.

SmartScreen verification is separate from signature validity. Test it from a clean Windows VM using the public GitHub release URL with Defender SmartScreen enabled. A valid signature proves identity and file integrity; reputation improves only after enough signed downloads/installs are trusted by Windows.

## Do Not Ship

- Do not make `install.ps1` the primary Windows install path.
- Do not advertise WinGet as available until the package is accepted in the default source.
- Do not publish public issues/comments containing local token, secret, or agent configuration details.
- Do not generate `SHA256SUMS.txt` or WinGet manifests before signing when signing is enabled.
