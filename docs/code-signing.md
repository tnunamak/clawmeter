# Code signing policy

Clawmeter's Windows artifacts are prepared for SignPath Foundation open-source code signing.

Current status: SignPath signing is not active until SignPath accepts the project and the repository is configured with the required GitHub secret and variables.

Once active, Windows release builds will use:

> Free code signing provided by SignPath.io, certificate by SignPath Foundation.

## Scope

The intended signed Windows release artifacts are:

- `clawmeter-windows-amd64.exe`
- `ClawmeterSetup.exe`

Other artifacts may be unsigned unless the release notes say otherwise.

## Source and Build Requirements

Signed Windows artifacts must be built by GitHub Actions from the public `tnunamak/clawmeter` repository.

The release workflow submits a GitHub Actions artifact to SignPath only after the Windows portable exe and installer have been built. SignPath origin verification can then verify repository, commit, workflow, and artifact origin metadata supplied by GitHub.

Checksums and WinGet metadata must be generated after signing, because Authenticode signing changes artifact bytes.

## Roles and Approval

Maintainer, code signing policy owner, and signing approver:

- `@tnunamak`

Maintainers must use multi-factor authentication on GitHub and SignPath accounts.

Every SignPath release signing request requires manual approval by an authorized signing approver before the signed artifact is released.

The repository uses `.github/CODEOWNERS` to require owner review for:

- `.signpath/`
- `.github/workflows/semantic-release.yml`
- `packaging/windows/`

GitHub must enforce this with an active branch ruleset on `main` that blocks force pushes and requires pull requests with code-owner review. That ruleset keeps the SignPath policy files, release workflow, and Windows packaging path review-gated before they can affect signed releases.

The SignPath source/build policy lives at `.signpath/policies/clawmeter/release-signing.yml`.

## Privacy

Clawmeter's runtime privacy behavior is documented in [Privacy Policy](../PRIVACY.md). Release signing submits build artifacts to SignPath; it does not give SignPath access to user credentials, provider tokens, local cache files, or runtime quota data.

## User Verification

All releases include `SHA256SUMS.txt`. Users can verify downloaded artifacts before running them:

```powershell
Get-FileHash .\ClawmeterSetup.exe -Algorithm SHA256
Select-String -Path .\SHA256SUMS.txt -Pattern "ClawmeterSetup.exe"
```

After signing is active, users can also verify Authenticode signatures:

```powershell
Get-AuthenticodeSignature .\ClawmeterSetup.exe | Format-List *
Get-AuthenticodeSignature .\clawmeter-windows-amd64.exe | Format-List *
```

Signatures prove publisher identity and artifact integrity. SmartScreen reputation is separate and may still build over time for new files.
