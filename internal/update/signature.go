package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"aead.dev/minisign"
)

const (
	sigSuffix = ".minisig"
	maxSig    = 4 << 10
)

// trustedKeys are the minisign public keys that may sign SHA256SUMS.txt.
// The backup key is kept offline for rotation. Tests replace this variable;
// nothing reads it from the environment or config.
var trustedKeys = mustParseKeys(primaryKey, backupKey)

const (
	primaryKey = "RWRW9JhIO6sI06w/ElmX9+E48kAw0MBjkIXrNX4TIJsiVG8SxAXvETXD" // ID D308AB3B4898F456
	backupKey  = "RWRP64iqRmyk168RgnOEbU9nhquh+eFjh67rSlTNwqo+3W5p4jZ4iK3+" // ID D7A46C46AA88EB4F
)

func mustParseKeys(texts ...string) []minisign.PublicKey {
	keys := make([]minisign.PublicKey, len(texts))
	for i, text := range texts {
		if err := keys[i].UnmarshalText([]byte(text)); err != nil {
			panic("update: bad embedded public key: " + err.Error())
		}
	}
	return keys
}

// TrustedComment is the signed comment a release signature must carry.
// Binding the tag stops an old signed release being replayed under a new tag.
func TrustedComment(tag string) string {
	return "clawmeter " + tag + " " + sumsName
}

// VerifySums checks that sig is a valid signature over sums by a trusted key
// and that its signed comment names tag. The key is chosen by the key ID in
// the signature; an unknown ID fails.
func VerifySums(sums, sig []byte, tag string) error {
	return VerifySumsWith(trustedKeys, sums, sig, tag)
}

// VerifySumsWith is VerifySums against an explicit key set, for tests.
func VerifySumsWith(keys []minisign.PublicKey, sums, sig []byte, tag string) error {
	var s minisign.Signature
	if err := s.UnmarshalText(sig); err != nil {
		return fmt.Errorf("signature: unparseable: %w", err)
	}
	for _, key := range keys {
		if key.ID() != s.KeyID {
			continue
		}
		// Verify also authenticates the trusted comment (global signature).
		if !minisign.Verify(key, sums, sig) {
			return errors.New("signature: invalid")
		}
		if want := TrustedComment(tag); s.TrustedComment != want {
			return fmt.Errorf("signature: signed comment %q, want %q", s.TrustedComment, want)
		}
		return nil
	}
	return fmt.Errorf("signature: unknown key ID %016X", s.KeyID)
}

func downloadSig(ctx context.Context, raw string) ([]byte, error) {
	resp, err := download(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("download signature (unsigned updates are refused): %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSig+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSig {
		return nil, errors.New("signature file too large")
	}
	return bytes.TrimSpace(data), nil
}
