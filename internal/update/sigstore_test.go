package update

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"google.golang.org/protobuf/encoding/protojson"
)

func signedFixture(t *testing.T, sums []byte, identity, issuer string) ([]byte, root.TrustedMaterial) {
	t.Helper()
	return signedFixtureWithSCT(t, sums, identity, issuer, true)
}

func signedFixtureWithSCT(t *testing.T, sums []byte, identity, issuer string, logged bool) ([]byte, root.TrustedMaterial) {
	t.Helper()
	virtual, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	entity, err := virtual.Sign(identity, issuer, sums)
	if err != nil {
		t.Fatal(err)
	}
	content, err := entity.VerificationContent()
	if err != nil {
		t.Fatal(err)
	}
	cert, trusted := loggedFixtureCertificate(t, content.Certificate(), virtual, logged)
	signature, err := entity.SignatureContent()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := entity.TlogEntries()
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0].TransparencyLogEntry()
	var body map[string]any
	if err := json.Unmarshal(entry.CanonicalizedBody, &body); err != nil {
		t.Fatal(err)
	}
	key := body["spec"].(map[string]any)["signature"].(map[string]any)["publicKey"].(map[string]any)
	key["content"] = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	entry.CanonicalizedBody, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	entry.LogIndex = 0
	entry.KindVersion = &protorekor.KindVersion{Kind: "hashedrekord", Version: "0.0.1"}
	set, err := virtual.RekorSignPayload(tlog.RekorPayload{
		Body: entry.CanonicalizedBody, IntegratedTime: entry.IntegratedTime,
		LogIndex: entry.LogIndex, LogID: hex.EncodeToString(entry.LogId.KeyId),
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.InclusionPromise = &protorekor.InclusionPromise{SignedEntryTimestamp: set}
	proof, err := virtual.GetInclusionProof(entry.CanonicalizedBody)
	if err != nil {
		t.Fatal(err)
	}
	rootHash, err := hex.DecodeString(*proof.RootHash)
	if err != nil {
		t.Fatal(err)
	}
	entry.InclusionProof = &protorekor.InclusionProof{
		LogIndex: *proof.LogIndex, TreeSize: *proof.TreeSize, RootHash: rootHash,
		Checkpoint: &protorekor.Checkpoint{Envelope: *proof.Checkpoint},
	}
	message := signature.MessageSignatureContent()
	digest := protocommon.HashAlgorithm_SHA2_256
	data, err := protojson.Marshal(&protobundle.Bundle{
		MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json",
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content:     &protobundle.VerificationMaterial_Certificate{Certificate: &protocommon.X509Certificate{RawBytes: cert.Raw}},
			TlogEntries: []*protorekor.TransparencyLogEntry{entry},
		},
		Content: &protobundle.Bundle_MessageSignature{MessageSignature: &protocommon.MessageSignature{
			MessageDigest: &protocommon.HashOutput{Algorithm: digest, Digest: message.Digest()}, Signature: message.Signature(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data, trusted
}

func TestApplySigstorePolicy(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong identity", "wrong issuer", "tampered sums", "tampered signature", "no log", "bad proof", "no signing time", "no certificate timestamp", "untrusted root", "oversized bundle"} {
		t.Run(scenario, func(t *testing.T) {
			exe, runs, renames := stubUpdate(t, validFixtureSums())
			identity, issuer := releaseIdentity, releaseIssuer
			if scenario == "wrong identity" {
				identity += "-other"
			}
			if scenario == "wrong issuer" {
				issuer += "/other"
			}
			sums := []byte(validFixtureSums())
			signature, trusted := signedFixtureWithSCT(t, sums, identity, issuer, scenario != "no certificate timestamp")
			if scenario == "tampered sums" {
				sums = append(sums, '\n')
			}
			if scenario == "no log" || scenario == "bad proof" || scenario == "no signing time" || scenario == "tampered signature" {
				var b protobundle.Bundle
				if err := protojson.Unmarshal(signature, &b); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "no log":
					b.VerificationMaterial.TlogEntries = nil
				case "bad proof":
					b.VerificationMaterial.TlogEntries[0].InclusionProof.RootHash[0] ^= 1
				case "no signing time":
					b.VerificationMaterial.TlogEntries[0].InclusionPromise = nil
				case "tampered signature":
					b.GetMessageSignature().Signature[0] ^= 1
				}
				var err error
				signature, err = protojson.Marshal(&b)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "untrusted root" {
				_, trusted = signedFixture(t, sums, identity, issuer)
			}
			if scenario == "oversized bundle" {
				signature = []byte(strings.Repeat(" ", (1<<20)+1))
			}
			verifySums = func(sums, signature []byte) error { return verifyChecksumSignatureWithRoot(sums, signature, trusted) }
			base := http.DefaultTransport
			http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
				switch filepath.Base(r.URL.Path) {
				case bundleName:
					return fixtureResponse(r, 200, string(signature)), nil
				case sumsName:
					return fixtureResponse(r, 200, string(sums)), nil
				default:
					return base.RoundTrip(r)
				}
			})
			oldChmod := chmodArtifact
			t.Cleanup(func() { chmodArtifact = oldChmod })
			chmods := 0
			chmodArtifact = func(path string, mode os.FileMode) error { chmods++; return os.Chmod(path, mode) }
			err := ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe)
			if scenario == "valid" {
				if err != nil || *runs != 1 || *renames == 0 || chmods != 1 {
					t.Fatalf("valid update failed: %v runs=%d renames=%d chmods=%d", err, *runs, *renames, chmods)
				}
			} else {
				if err == nil || *runs != 0 || *renames != 0 || chmods != 0 {
					t.Fatalf("unsafe update accepted: %v runs=%d renames=%d chmods=%d", err, *runs, *renames, chmods)
				}
				assertOriginal(t, exe)
			}
		})
	}
}

func TestEmbeddedSigstoreRoot(t *testing.T) {
	trusted, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(trusted.FulcioCertificateAuthorities()) == 0 || len(trusted.RekorLogs()) == 0 {
		t.Fatal("embedded root missing Fulcio/Rekor trust")
	}
	if err := VerifyChecksumSignature([]byte(validFixtureSums()), []byte("{}")); err == nil {
		t.Fatal("empty bundle accepted")
	}
}

func TestApplyRequiresSigstoreBundle(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "malformed"}[missing], func(t *testing.T) {
			exe, runs, renames := stubUpdate(t, validFixtureSums())
			oldChmod := chmodArtifact
			t.Cleanup(func() { chmodArtifact = oldChmod })
			chmods := 0
			chmodArtifact = func(path string, mode os.FileMode) error { chmods++; return os.Chmod(path, mode) }
			base := http.DefaultTransport
			http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, ".sigstore.json") {
					if missing {
						return fixtureResponse(r, 404, ""), nil
					}
					return fixtureResponse(r, 200, "invalid bundle"), nil
				}
				return base.RoundTrip(r)
			})
			if err := ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe); err == nil {
				t.Fatal("unsigned update accepted")
			}
			if *runs != 0 || *renames != 0 || chmods != 0 {
				t.Fatalf("unsigned update reached chmod/execution/replacement: %d/%d/%d", chmods, *runs, *renames)
			}
			assertOriginal(t, exe)
		})
	}
}
