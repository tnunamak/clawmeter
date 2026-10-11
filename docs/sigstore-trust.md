# Sigstore trust snapshot

The updater embeds `internal/update/trusted_root.json`. It never fetches trust
material during verification. The snapshot was obtained on 2026-10-11 from the
Sigstore public-good TUF repository (`https://tuf-repo-cdn.sigstore.dev`) through
sigstore-go's authenticated TUF chain, rooted in that dependency's embedded TUF
root. It contains Fulcio, Rekor, certificate-transparency and timestamp-authority
trust material.

Snapshot SHA-256:
`6494e21ea73fa7ee769f85f57d5a3e6a08725eae1e38c755fc3517c9e6bc0b66`.

From the repository root, refresh it with:

```sh
go run ./tools/refresh-sigstore-root
sha256sum internal/update/trusted_root.json
go test ./internal/update
```

The refresh tool uses a fresh TUF client without a local cache. TUF verifies root
rotation, metadata signatures, expiry and target hashes before the tool writes
the snapshot. Review the changed keys and validity periods, record the retrieval
date and hash here, and commit the snapshot with the application. Do not replace
it with a root downloaded alongside an unverified release. If TUF refresh fails,
stop and investigate the repository or the dependency's bootstrap root.

The workflow signs checksum bytes with cosign v3 and verifies the bundle using
`go run ./tools/verify-release SHA256SUMS.txt SHA256SUMS.txt.sigstore.json`.
This runs the updater's offline policy, so a service rotation unknown to the
snapshot blocks publication as well as updating. Only the upload/signing job
has `id-token: write`; the publish job verifies downloaded assets without that
permission before it publishes the draft.

A frozen snapshot cannot learn new keys or revocations. Old clients can become
unable to update after a rotation; refresh before that happens. A stranded client
needs a manually verified installation with current trust material. This change
does not add independently authenticated runtime root rotation, release-version
binding inside the signed checksum file, or TUF release freshness protection.

The shell and PowerShell installers retain their existing checksum checks.
Users can apply the cosign instructions in `SECURITY.md` before manual installs.
