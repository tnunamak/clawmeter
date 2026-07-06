# WinGet Packaging

WinGet is the second Windows install path. It installs the same `ClawmeterSetup.exe` that users can download directly from GitHub.

```powershell
winget install --id tnunamak.Clawmeter -e
```

Until Microsoft accepts the package into the default source, the command above is not expected to work for normal users. The direct installer remains the primary Windows path.

## Release Flow

1. Publish a release that includes `ClawmeterSetup.exe`.
2. Verify the public GitHub release URL downloads the same bytes used for manifest generation.
3. Generate the manifest:

```bash
packaging/winget/generate.sh vX.Y.Z
```

4. Submit or update the WinGet PR:

```bash
packaging/winget/submit-pr.sh vX.Y.Z
```

The release workflow does this automatically when `WINGET_PR_TOKEN` is configured. The token must be able to push to the `tnunamak/winget-pkgs` fork and open pull requests against `microsoft/winget-pkgs`.

The first accepted package appears as `New package`; later releases appear as `New version`. If the package has not been accepted yet and a first-package PR is already open, automation skips opening another one. Use `WINGET_ALLOW_DUPLICATE_NEW_PACKAGE_PR=1` only when intentionally superseding an open first-package PR.

After the package exists upstream, release automation also closes any stale open `New package` PRs authored by the fork owner. Rehearse that cleanup without changing GitHub:

```bash
packaging/winget/close-superseded-first-package-prs.sh --dry-run --latest-tag vX.Y.Z
```

## Local Rehearsal

Generate a manifest against an existing release asset:

```bash
packaging/winget/generate.sh vX.Y.Z
```

Generate a manifest against a local installer before release upload:

```bash
WINGET_ASSET_PATH=/path/to/ClawmeterSetup.exe packaging/winget/generate.sh vX.Y.Z
```

Dry-run PR automation without pushing:

```bash
WINGET_DRY_RUN=1 packaging/winget/submit-pr.sh vX.Y.Z
```

On Windows:

```powershell
$version = "X.Y.Z"
$manifest = ".\packaging\winget\out\manifests\t\tnunamak\Clawmeter\$version"
winget validate --manifest $manifest --disable-interactivity
winget settings --enable LocalManifestFiles
winget install --manifest $manifest --silent --accept-source-agreements --accept-package-agreements
winget list --id tnunamak.Clawmeter -e
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

That proves the WinGet client can parse the manifest, verify SHA256, run the silent Inno installer, expose `clawmeter`, and uninstall it. It does not prove public WinGet acceptance, SmartScreen reputation, or code-signing reputation.

After Microsoft merges the package:

```powershell
winget source update
winget show --id tnunamak.Clawmeter -e --source winget
winget install --id tnunamak.Clawmeter -e --source winget --silent
clawmeter providers
winget uninstall --id tnunamak.Clawmeter -e
```

Or run the repeatable verifier:

```powershell
.\packaging\windows\verify-winget.ps1 -ExpectedVersion X.Y.Z
```

## Legacy Portable Manifest

Old releases without `ClawmeterSetup.exe` can still generate a local portable manifest for debugging:

```bash
packaging/winget/generate.sh v0.21.2 portable
```

The full Windows release plan lives in [../windows/PLAN.md](../windows/PLAN.md).
