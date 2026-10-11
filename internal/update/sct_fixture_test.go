package update

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"testing"
	"time"

	ct "github.com/google/certificate-transparency-go"
	"github.com/google/certificate-transparency-go/tls"
	ctx509 "github.com/google/certificate-transparency-go/x509"
	"github.com/google/certificate-transparency-go/x509util"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
)

type fixtureTrust struct {
	root.TrustedMaterial
	fulcio *root.FulcioCertificateAuthority
	ctlog  *root.TransparencyLog
}

func (f fixtureTrust) FulcioCertificateAuthorities() []root.CertificateAuthority {
	return []root.CertificateAuthority{f.fulcio}
}

func (f fixtureTrust) CTLogs() map[string]*root.TransparencyLog {
	return map[string]*root.TransparencyLog{hex.EncodeToString(f.ctlog.ID): f.ctlog}
}

// Reissue the fixture's signing key under a local CA, with a locally signed SCT.
// VirtualSigstore supplies Rekor, but does not generate certificate timestamps.
func loggedFixtureCertificate(t *testing.T, original *x509.Certificate, trusted root.TrustedMaterial, logged bool) (*x509.Certificate, root.TrustedMaterial) {
	t.Helper()
	parent, issuerKey, err := ca.GenerateRootCa()
	if err != nil {
		t.Fatal(err)
	}
	ctKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(ctKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	logID := sha256.Sum256(publicKey)
	trusted = fixtureTrust{
		TrustedMaterial: trusted,
		fulcio:          &root.FulcioCertificateAuthority{Root: parent},
		ctlog: &root.TransparencyLog{ID: logID[:], PublicKey: ctKey.Public(), HashFunc: crypto.SHA256,
			ValidityPeriodStart: time.Now().Add(-time.Hour), ValidityPeriodEnd: time.Now().Add(time.Hour)},
	}
	template := *original
	template.AuthorityKeyId = nil
	for _, extension := range original.Extensions {
		if extension.Id.Equal(asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}) {
			template.ExtraExtensions = append(template.ExtraExtensions, extension)
		}
	}
	issue := func() *x509.Certificate {
		t.Helper()
		der, err := x509.CreateCertificate(rand.Reader, &template, parent, original.PublicKey, issuerKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	precert := issue()
	if !logged {
		return precert, trusted
	}
	sct := ct.SignedCertificateTimestamp{SCTVersion: ct.V1, LogID: ct.LogID{KeyID: logID}, Timestamp: uint64(time.Now().UnixMilli())}
	entry := ct.LogEntry{Leaf: ct.MerkleTreeLeaf{Version: ct.V1, LeafType: ct.TimestampedEntryLeafType,
		TimestampedEntry: &ct.TimestampedEntry{Timestamp: sct.Timestamp, EntryType: ct.PrecertLogEntryType,
			PrecertEntry: &ct.PreCert{IssuerKeyHash: sha256.Sum256(parent.RawSubjectPublicKeyInfo), TBSCertificate: precert.RawTBSCertificate}}}}
	input, err := ct.SerializeSCTSignatureInput(sct, entry)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	signature, err := ecdsa.SignASN1(rand.Reader, ctKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sct.Signature = ct.DigitallySigned{Algorithm: tls.SignatureAndHashAlgorithm{Hash: tls.SHA256, Signature: tls.ECDSA}, Signature: signature}
	list, err := x509util.MarshalSCTsIntoSCTList([]*ct.SignedCertificateTimestamp{&sct})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := tls.Marshal(*list)
	if err != nil {
		t.Fatal(err)
	}
	value, err := asn1.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	template.ExtraExtensions = append(template.ExtraExtensions, pkix.Extension{Id: asn1.ObjectIdentifier(ctx509.OIDExtensionCTSCT), Value: value})
	return issue(), trusted
}
