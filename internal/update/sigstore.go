package update

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	releaseIdentity = "https://github.com/" + repo + "/.github/workflows/semantic-release.yml@refs/heads/main"
	releaseIssuer   = "https://token.actions.githubusercontent.com"
)

//go:embed trusted_root.json
var trustedRootJSON []byte

// Tests substitute a local CA; production always uses the embedded snapshot.
var verifySums = VerifyChecksumSignature

// VerifyChecksumSignature verifies release checksums offline using the embedded
// trust snapshot and the release workflow's exact identity.
func VerifyChecksumSignature(sums, signature []byte) error {
	trusted, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		return fmt.Errorf("load embedded Sigstore root: %w", err)
	}
	return verifyChecksumSignatureWithRoot(sums, signature, trusted)
}

func verifyChecksumSignatureWithRoot(sums, signature []byte, trusted root.TrustedMaterial) error {
	var signed bundle.Bundle
	if err := signed.UnmarshalJSON(signature); err != nil {
		return fmt.Errorf("parse Sigstore bundle: %w", err)
	}
	verifier, err := verify.NewVerifier(trusted, verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1), verify.WithSignedCertificateTimestamps(1))
	if err != nil {
		return err
	}
	identity, err := verify.NewShortCertificateIdentity(releaseIssuer, "", releaseIdentity, "")
	if err != nil {
		return err
	}
	_, err = verifier.Verify(&signed, verify.NewPolicy(verify.WithArtifact(bytes.NewReader(sums)), verify.WithCertificateIdentity(identity)))
	return err
}
