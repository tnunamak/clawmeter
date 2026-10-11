# Security Policy

## Supported Versions

Security fixes are shipped in the latest Clawmeter release. Please upgrade to the latest release before reporting an issue.

## Reporting a Vulnerability

Do not report credential leaks, token contents, or exploitable security details in public issues, pull requests, release comments, or discussions.

Use a private GitHub security advisory for `tnunamak/clawmeter`, or contact the maintainer privately if GitHub advisories are unavailable.

## Credential Handling

Clawmeter reads existing local provider credentials only to fetch usage, quota, account, or rate-limit status. Some OAuth providers may refresh access tokens and write updated tokens to the provider's normal local credential file. See [Privacy Policy](PRIVACY.md) for provider-specific behavior.

Clawmeter does not operate a backend service and does not collect provider credentials.

## Release Integrity

Release assets include `SHA256SUMS.txt` and its keyless Sigstore bundle, `SHA256SUMS.txt.sigstore.json`; with cosign v3, verify the downloaded checksum file using the command below, then check your downloaded binary against those authenticated checksums.
The self-updater verifies the exact release-workflow identity, GitHub OIDC issuer, transparency-log evidence and an authenticated signing time against embedded Sigstore trust material, then checks the binary's SHA-256 before chmod, execution or replacement; missing or invalid bundles stop the update.
Older releases have no bundle, and an older client can bootstrap the first release containing this verifier only through its existing SHA-256 check; clients containing the verifier require bundles for every subsequent update.

```sh
cosign verify-blob \
  --bundle SHA256SUMS.txt.sigstore.json \
  --certificate-identity 'https://github.com/tnunamak/clawmeter/.github/workflows/semantic-release.yml@refs/heads/main' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  SHA256SUMS.txt
sha256sum --ignore-missing --check SHA256SUMS.txt
```

On macOS use `shasum -a 256 --check SHA256SUMS.txt` for the checksum check (missing assets report errors); on Windows compare `Get-FileHash -Algorithm SHA256` with the authenticated entry for your binary.
Maintainers: [refresh the embedded trust snapshot](docs/sigstore-trust.md) before Sigstore rotates signing services.

Windows code signing is not active yet. The current code signing policy is documented in [docs/code-signing.md](docs/code-signing.md).
