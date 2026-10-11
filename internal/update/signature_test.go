package update

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aead.dev/minisign"
	"golang.org/x/crypto/blake2b"
)

var testPrimary, testBackup = mustGenerate(), mustGenerate()

func mustGenerate() minisign.PrivateKey {
	_, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return priv
}

func pubOf(k minisign.PrivateKey) minisign.PublicKey { return k.Public().(minisign.PublicKey) }

func TestMain(m *testing.M) {
	trustedKeys = []minisign.PublicKey{pubOf(testPrimary), pubOf(testBackup)}
	os.Exit(m.Run())
}

// signSums signs prehashed, as CI does.
func signSums(k minisign.PrivateKey, sums []byte, comment string) []byte {
	r := minisign.NewReader(bytes.NewReader(sums))
	if _, err := io.Copy(io.Discard, r); err != nil {
		panic(err)
	}
	return r.SignWithComments(k, comment, "untrusted")
}

func TestEmbeddedKeyIDs(t *testing.T) {
	keys := mustParseKeys(primaryKey, backupKey)
	for i, want := range []string{"D308AB3B4898F456", "D7A46C46AA88EB4F"} {
		if got := fmt.Sprintf("%016X", keys[i].ID()); got != want {
			t.Errorf("key %d ID = %s, want %s", i, got, want)
		}
	}
}

func TestVerifySums(t *testing.T) {
	sums := []byte("abc  clawmeter-linux-amd64\n")
	comment := TrustedComment("v1.2.3")
	if comment != "clawmeter v1.2.3 SHA256SUMS.txt" {
		t.Fatalf("comment = %q", comment)
	}
	good := signSums(testPrimary, sums, comment)

	spoofed := func() []byte {
		var s minisign.Signature
		if err := s.UnmarshalText(signSums(mustGenerate(), sums, comment)); err != nil {
			t.Fatal(err)
		}
		s.KeyID = testPrimary.ID()
		out, err := s.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}()
	tamperedComment := []byte(strings.Replace(string(good), "trusted comment: "+comment, "trusted comment: clawmeter v9.9.9 SHA256SUMS.txt", 1))
	if string(tamperedComment) == string(good) {
		t.Fatal("test did not tamper the trusted comment")
	}

	for _, tc := range []struct {
		name string
		sums []byte
		sig  []byte
		tag  string
		want string // empty means success
	}{
		{"primary", sums, good, "v1.2.3", ""},
		{"backup", sums, signSums(testBackup, sums, comment), "v1.2.3", ""},
		{"unknown key id", sums, signSums(mustGenerate(), sums, comment), "v1.2.3", "unknown key ID"},
		{"wrong key with spoofed id", sums, spoofed, "v1.2.3", "invalid"},
		{"tampered sums", []byte("abd  clawmeter-linux-amd64\n"), good, "v1.2.3", "invalid"},
		{"tampered trusted comment", sums, tamperedComment, "v9.9.9", "invalid"},
		{"tag mismatch", sums, good, "v1.2.4", "signed comment"},
		{"garbage", sums, []byte("not a signature"), "v1.2.3", "unparseable"},
		{"empty", sums, nil, "v1.2.3", "unparseable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySums(tc.sums, tc.sig, tc.tag)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// signedApply runs ApplyTo against a fake release whose signature response is
// chosen by the caller, and reports whether anything ran or was replaced.
func signedApply(t *testing.T, sigResponse func(r *http.Request, sums string) *http.Response) (err error, exe string, runs, renames *int) {
	t.Helper()
	asset := assetNameFor(runtime.GOOS, runtime.GOARCH)
	payload := "verified fixture, never executed"
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(payload)), asset)
	exe, runs, renames = stubUpdate(t, sums)
	http.DefaultTransport = rawTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, sigSuffix):
			return sigResponse(r, sums), nil
		case strings.HasSuffix(r.URL.Path, "/"+sumsName):
			return fixtureResponse(r, 200, sums), nil
		}
		return fixtureResponse(r, 200, payload), nil
	})
	err = ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/"+asset, exe)
	return err, exe, runs, renames
}

func TestApplyRequiresValidSignature(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix executable fixtures")
	}
	comment := func(tag string) string { return TrustedComment(tag) }
	for _, tc := range []struct {
		name string
		resp func(r *http.Request, sums string) *http.Response
		want string
	}{
		{"missing 404", func(r *http.Request, _ string) *http.Response { return fixtureResponse(r, 404, "") }, "unsigned updates are refused"},
		{"oversized", func(r *http.Request, _ string) *http.Response {
			return fixtureResponse(r, 200, strings.Repeat("x", maxSig+1))
		}, "too large"},
		{"unparseable", func(r *http.Request, _ string) *http.Response { return fixtureResponse(r, 200, "nope") }, "unparseable"},
		{"unknown key", func(r *http.Request, s string) *http.Response {
			return fixtureResponse(r, 200, string(signSums(mustGenerate(), []byte(s), comment("v2.0.0"))))
		}, "unknown key ID"},
		{"older tag replayed", func(r *http.Request, s string) *http.Response {
			return fixtureResponse(r, 200, string(signSums(testPrimary, []byte(s), comment("v1.0.0"))))
		}, "signed comment"},
		{"signature for other sums", func(r *http.Request, _ string) *http.Response {
			return fixtureResponse(r, 200, string(signSums(testPrimary, []byte("other\n"), comment("v2.0.0"))))
		}, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, exe, runs, renames := signedApply(t, tc.resp)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if *runs != 0 || *renames != 0 {
				t.Fatalf("unsafe side effect: executions=%d renames=%d", *runs, *renames)
			}
			assertOriginal(t, exe)
		})
	}
}

func TestApplyAcceptsSignedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix executable fixtures")
	}
	for name, key := range map[string]minisign.PrivateKey{"primary": testPrimary, "backup": testBackup} {
		t.Run(name, func(t *testing.T) {
			err, exe, runs, _ := signedApply(t, func(r *http.Request, s string) *http.Response {
				return fixtureResponse(r, 200, string(signSums(key, []byte(s), TrustedComment("v2.0.0"))))
			})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := os.ReadFile(exe)
			if readErr != nil || string(got) != "verified fixture, never executed" || *runs != 1 {
				t.Fatalf("not installed: %q %v runs=%d", got, readErr, *runs)
			}
			if files, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".clawmeter-update-*")); len(files) != 0 {
				t.Fatalf("staging files leaked: %v", files)
			}
		})
	}
}

// The algorithm field (Ed vs ED) is outside both signed messages, so a
// prehashed CI signature can be relabelled as legacy. Accepting that would
// authenticate BLAKE2b-512(sums), bytes the key holder never signed.
func TestVerifySumsRejectsRelabelledLegacySignature(t *testing.T) {
	sums := []byte("0000000000000000000000000000000000000000000000000000000000000000  clawmeter-linux-amd64\n")
	sig := signSums(testPrimary, sums, TrustedComment("v2.0.0"))
	if err := VerifySums(sums, sig, "v2.0.0"); err != nil {
		t.Fatalf("genuine signature rejected: %v", err)
	}
	var s minisign.Signature
	if err := s.UnmarshalText(sig); err != nil {
		t.Fatal(err)
	}
	s.Algorithm = minisign.EdDSA
	forged, err := s.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	h := blake2b.Sum512(sums)
	if err := VerifySums(h[:], forged, "v2.0.0"); err == nil {
		t.Fatal("VerifySums accepted a relabelled legacy signature over BLAKE2b(sums)")
	}
	if err := VerifySums(sums, minisign.SignWithComments(testPrimary, sums, TrustedComment("v2.0.0"), ""), "v2.0.0"); err == nil {
		t.Fatal("VerifySums accepted a legacy (non-prehashed) signature")
	}
}
