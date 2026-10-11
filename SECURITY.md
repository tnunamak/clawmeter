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

Release assets include checksums in `SHA256SUMS.txt`.

### Verify a download

Releases from v0.40.0 on are signed. `SHA256SUMS.txt` carries a [minisign](https://jedisct1.github.io/minisign/) signature in `SHA256SUMS.txt.minisig`. Download both files from the release, then run:

```bash
minisign -Vm SHA256SUMS.txt -P RWRW9JhIO6sI06w/ElmX9+E48kAw0MBjkIXrNX4TIJsiVG8SxAXvETXD
sha256sum --check --ignore-missing SHA256SUMS.txt
```

The signed comment names the release, for example `clawmeter v0.40.0 SHA256SUMS.txt`.

The self-updater checks this signature with keys built into the binary. It refuses unsigned updates. A backup key is kept offline for key rotation.

Windows code signing is not active yet. The current code signing policy is documented in [docs/code-signing.md](docs/code-signing.md).
