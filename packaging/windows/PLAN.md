# Windows Distribution Plan

Windows should feel like a normal desktop product, not a developer script.

Preferred user journey:

1. Direct installer first: download `ClawmeterSetup.exe` from the GitHub release.
2. WinGet second: `winget install --id tnunamak.Clawmeter -e` after Microsoft accepts the package.
3. Portable fallback: download `clawmeter-windows-amd64.exe` for locked-down machines and debugging.
4. Signing later: wait until the project has more public reputation before applying to SignPath Foundation; do not pay for code signing unless that decision changes.

## Installer Requirements

The installer must:

- Install per-user into `%LOCALAPPDATA%\Programs\Clawmeter`.
- Create a Start Menu shortcut for `clawmeter tray`.
- Put `clawmeter` on the user `PATH`.
- Register an uninstall entry.
- Offer launch-at-login as an opt-in installer task.
- Offer automatic update checks as an installer task, checked by default so the user can opt out.
- Display the privacy policy before installation.
- Launch the tray after interactive installs.
- Avoid terminal flashes for tray actions such as Refresh Now.
- Clean up the shortcut, Run key, PATH entry, and install directory on uninstall.

WinGet is a distribution channel for the same installer, not a separate product shape. The portable exe is intentionally less polished and must stay documented as an advanced fallback.

## Local Installer Build

```powershell
go build -tags tray -o C:\temp\clawmeter.exe .\cmd\clawmeter
choco install innosetup --no-progress -y
.\packaging\windows\build-inno.ps1 -BinaryPath C:\temp\clawmeter.exe -Version 0.0.0-test -OutputDir C:\temp
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -IncludeStartup
.\packaging\windows\verify-installer.ps1 -InstallerPath C:\temp\ClawmeterSetup.exe -DisableUpdates
```

## Release Verification

Before publishing or updating WinGet, verify the final public release asset in a clean Windows VM:

```powershell
$version = "0.22.0"
$base = "https://github.com/tnunamak/clawmeter/releases/download/v$version"
Invoke-WebRequest "$base/ClawmeterSetup.exe" -OutFile ClawmeterSetup.exe
Invoke-WebRequest "$base/SHA256SUMS.txt" -OutFile SHA256SUMS.txt
Get-FileHash .\ClawmeterSetup.exe -Algorithm SHA256
Select-String -Path .\SHA256SUMS.txt -Pattern "ClawmeterSetup.exe"
```

Repeatable VM verifier:

```powershell
.\packaging\windows\verify-release.ps1 -Version 0.22.0 -IncludeStartup
.\packaging\windows\verify-release.ps1 -Version 0.22.0 -DisableUpdates -ScanWithDefender
```

VM checklist:

- Installer wizard opens without a PowerShell download command.
- Publisher/trust prompt is understandable for unsigned builds, and Authenticode details are visible after signing is active.
- Default install creates `%LOCALAPPDATA%\Programs\Clawmeter\clawmeter.exe`.
- Start Menu shortcut launches the tray.
- A new PowerShell can run `clawmeter providers`.
- Bare `clawmeter` produces useful output when piped: `clawmeter | Tee-Object clawmeter.log`.
- Refresh Now does not visibly flash a terminal window.
- Launch-at-login can be enabled and disabled from the tray.
- Automatic update checks can be disabled during install or later with `clawmeter config set check_for_updates false`.
- Uninstall removes install directory executable, Start Menu shortcut, startup Run key, and PATH entry.

Defender / Windows Security checklist:

- Run `collect-defender-evidence.ps1` only when Windows Security actually flags an installer or executable.
- Keep the evidence folder local until it has been reviewed; it can include machine policy details.
- Submit confirmed false positives through Microsoft's file submission portal: <https://www.microsoft.com/en-us/wdsi/filesubmission>.

## WinGet

Local rehearsal before or immediately after release upload:

```bash
WINGET_ASSET_PATH=/path/to/ClawmeterSetup.exe packaging/winget/generate.sh vX.Y.Z
WINGET_DRY_RUN=1 packaging/winget/submit-pr.sh vX.Y.Z
```

On Windows with local manifest mode:

```powershell
$version = "0.22.0"
$manifest = ".\packaging\winget\out\manifests\t\tnunamak\Clawmeter\$version"
winget validate --manifest $manifest --disable-interactivity --enable LocalManifestFiles
winget install --manifest $manifest --silent tnunamak.Clawmeter -e
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

Before opening or updating the WinGet PR, CI must verify that the public release URL bytes match the installer used to generate the manifest hash:

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

PR behavior: the first accepted submission is `New package`; later releases become `New version`. Red closed PRs in `microsoft/winget-pkgs` are rejected, superseded, or abandoned; accepted PRs merge. `wingetbot` validation runs in Azure DevOps, not GitHub Actions, so GitHub may show little beyond CLA status.

## Portable Fallback

The portable exe remains useful for locked-down machines and debugging. It is not the recommended Windows onboarding path.

```powershell
Invoke-WebRequest "$base/clawmeter-windows-amd64.exe" -OutFile clawmeter.exe
Get-FileHash .\clawmeter.exe -Algorithm SHA256
.\clawmeter.exe providers
.\clawmeter.exe --json
```

Portable verification should prove the exe runs and reads existing credentials, but it does not need Start Menu, PATH, launch-at-login, or uninstall behavior.

## SignPath Foundation Signing

Do not apply to SignPath Foundation yet. Revisit this when Clawmeter has more public reputation. Until accepted and configured, Windows artifacts remain unsigned and the release workflow must keep publishing normal unsigned artifacts.

Repo collateral for the application:

- [Code signing policy](../../docs/code-signing.md)
- [Privacy policy](../../PRIVACY.md)
- [Security policy](../../SECURITY.md)
- [Third-party components](../../docs/third-party-components.md)
- `.signpath/artifact-configurations/windows-release.xml`
- `.signpath/policies/clawmeter/release-signing.yml`
- `.github/CODEOWNERS`

GitHub configuration after SignPath acceptance:

- Secret: `SIGNPATH_API_TOKEN`
- Variable: `SIGNPATH_ENABLED=true`
- Variable: `SIGNPATH_ORGANIZATION_ID`
- Variable: `SIGNPATH_PROJECT_SLUG`
- Variable: `SIGNPATH_SIGNING_POLICY_SLUG`
- Variable: `SIGNPATH_ARTIFACT_CONFIGURATION_SLUG`

The release workflow intentionally requires `SIGNPATH_ENABLED=true`. If that variable is unset or false, releases stay unsigned. If it is true and any required SignPath setting is missing, the release fails instead of silently publishing unsigned Windows artifacts.

Signing flow:

1. Build unsigned `clawmeter-windows-amd64.exe`.
2. Build unsigned `ClawmeterSetup.exe`.
3. Upload both files as a GitHub Actions artifact.
4. Submit that artifact to `SignPath/github-action-submit-signing-request@v2`.
5. Wait for signing completion and replace the Windows artifacts with signed outputs.
6. Generate WinGet manifest bundle and `SHA256SUMS.txt` after signing.
7. Upload final artifacts to the GitHub release.

The SignPath ZIP artifact configuration signs the portable exe and setup exe. The existing Inno Setup signing hooks remain useful if a local or remote signing tool is used later for deeper installer/uninstaller signing; do not claim the SignPath artifact configuration signs the generated Inno uninstaller unless that has been verified.

Signing verification:

```powershell
Get-AuthenticodeSignature .\clawmeter-windows-amd64.exe | Format-List *
Get-AuthenticodeSignature .\ClawmeterSetup.exe | Format-List *
signtool verify /pa /tw /v .\clawmeter-windows-amd64.exe
signtool verify /pa /tw /v .\ClawmeterSetup.exe
.\packaging\windows\verify-installer.ps1 -InstallerPath .\ClawmeterSetup.exe -IncludeStartup -ExpectSigned
```

SmartScreen verification is separate from signature validity. Test from a clean Windows VM using the public GitHub release URL with Defender SmartScreen enabled. A valid signature proves publisher identity and artifact integrity; reputation improves only after enough signed downloads and installs are trusted by Windows.

## Do Not

- Do not make `install.ps1` the primary Windows install path.
- Do not advertise WinGet as available until the package is accepted.
- Do not publish public issues or comments containing local token, secret, or agent configuration details.
- Do not generate `SHA256SUMS.txt` or WinGet manifests before signing when signing is enabled.
