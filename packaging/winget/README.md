# WinGet Packaging

Clawmeter's public Windows package installs the same desktop product as the
direct download: `ClawmeterSetup.exe`.

```powershell
winget install --id tnunamak.Clawmeter -e
```

That path gives users the normal Windows experience: Start Menu shortcut,
uninstall entry, app icon, user-level install directory, `clawmeter` on `PATH`,
optional launch-at-login, and the interactive tray app. The portable
`clawmeter-windows-amd64.exe` remains available for advanced/manual use, but it
is not the primary Windows onboarding path.

## Release Flow

1. Publish a Clawmeter release that includes `ClawmeterSetup.exe`.
2. The release workflow uploads `winget-manifest-<version>.zip`.
3. Submit or update the WinGet PR:

```bash
packaging/winget/submit-pr.sh vX.Y.Z
```

The release workflow runs the same script automatically when the
`WINGET_PR_TOKEN` repository secret is configured. That token must be able to
push to `tnunamak/winget-pkgs` and open pull requests against
`microsoft/winget-pkgs`.

Until the Microsoft PR is merged, Clawmeter is not publicly installable through
the default WinGet source.

## Local Rehearsal

Generate a manifest against an existing release asset:

```bash
packaging/winget/generate.sh v0.0.0
```

Generate a manifest against a local installer before release upload:

```bash
WINGET_ASSET_PATH=/path/to/ClawmeterSetup.exe packaging/winget/generate.sh v0.0.0
```

Dry-run the PR automation without pushing:

```bash
WINGET_DRY_RUN=1 packaging/winget/submit-pr.sh v0.0.0
```

On Windows:

```powershell
winget settings --enable LocalManifestFiles
winget validate .\packaging\winget\out\manifests\t\tnunamak\Clawmeter\<version>
winget install --manifest .\packaging\winget\out\manifests\t\tnunamak\Clawmeter\<version> --silent --accept-source-agreements --accept-package-agreements
clawmeter providers
winget uninstall --id tnunamak.Clawmeter
```

That proves the WinGet client can parse the manifest, verify SHA256, run the
silent Inno installer, expose `clawmeter`, and uninstall it. It does not prove
public WinGet acceptance, SmartScreen reputation, or code-signing reputation.

## Legacy Portable Manifest

Old releases without `ClawmeterSetup.exe` can still generate a local portable
manifest for debugging:

```bash
packaging/winget/generate.sh v0.21.2 portable
```
