# WinGet Packaging

Clawmeter's public Windows package should install the same product as the
direct download: `ClawmeterSetup.exe`.

```powershell
winget install --id tnunamak.Clawmeter -e
```

That path should give users the Start Menu shortcut, uninstall entry, app icon,
user-level install directory, `clawmeter` on `PATH`, optional launch-at-login,
and the interactive tray app. The portable `clawmeter-windows-amd64.exe` stays
available for advanced/manual use, but it is not the primary Windows onboarding
path.

## Release Flow

1. Publish a Clawmeter release that includes `ClawmeterSetup.exe`.
2. The release workflow uploads `winget-manifest-<version>.zip`.
3. Submit that manifest directory to `microsoft/winget-pkgs` at:

```text
manifests/t/tnunamak/Clawmeter/<version>/
```

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

On Windows:

```powershell
winget settings --enable LocalManifestFiles
winget validate .\packaging\winget\out\manifests\t\tnunamak\Clawmeter\<version>
winget install --manifest .\packaging\winget\out\manifests\t\tnunamak\Clawmeter\<version> --silent --accept-source-agreements
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

This proves the WinGet client can parse the manifest, verify SHA256, run the
silent Inno installer, expose `clawmeter`, and uninstall it. It does not prove
public WinGet acceptance, SmartScreen reputation, or code-signing reputation.

## Legacy Portable Manifest

Old releases without `ClawmeterSetup.exe` can still generate a local portable
manifest for debugging:

```bash
packaging/winget/generate.sh v0.21.2 portable
```
