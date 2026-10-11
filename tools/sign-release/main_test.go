package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aead.dev/minisign"
	"github.com/tnunamak/clawmeter/internal/update"
)

func TestSignThenVerify(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := minisign.EncryptKey("pw", priv)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAWMETER_MINISIGN_KEY": string(encrypted), "CLAWMETER_MINISIGN_PASSWORD": "pw"}
	getenv := func(k string) string { return env[k] }

	old := verifySums
	t.Cleanup(func() { verifySums = old })
	verifySums = func(sums, sig []byte, tag string) error {
		return update.VerifySumsWith([]minisign.PublicKey{pub}, sums, sig, tag)
	}

	file := filepath.Join(t.TempDir(), "SHA256SUMS.txt")
	if err := os.WriteFile(file, []byte("abc  clawmeter-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", file, "v0.40.0"}, getenv); err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(file + ".minisig")
	if err != nil || !strings.Contains(string(sig), "trusted comment: clawmeter v0.40.0 SHA256SUMS.txt") {
		t.Fatalf("unexpected signature file: %q %v", sig, err)
	}
	if err := run([]string{"verify", file, "v0.40.0"}, getenv); err != nil {
		t.Fatal(err)
	}
	var parsed minisign.Signature
	if err := parsed.UnmarshalText(sig); err != nil || parsed.Algorithm != minisign.HashEdDSA {
		t.Fatalf("signature algorithm = %v (err %v), want prehashed HashEdDSA", parsed.Algorithm, err)
	}
	if err := run([]string{"verify", file, "v0.40.1"}, getenv); err == nil {
		t.Fatal("verify accepted the wrong tag")
	}

	// With the embedded production keys, a throwaway signature must be refused.
	verifySums = old
	if err := run([]string{"verify", file, "v0.40.0"}, getenv); err == nil {
		t.Fatal("verify accepted a signature from an untrusted key")
	}

	if err := os.WriteFile(file, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verifySums = func(sums, sig []byte, tag string) error {
		return update.VerifySumsWith([]minisign.PublicKey{pub}, sums, sig, tag)
	}
	if err := run([]string{"verify", file, "v0.40.0"}, getenv); err == nil {
		t.Fatal("verify accepted tampered file")
	}
}

func TestSignRequiresSecrets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "SHA256SUMS.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{
		{},
		{"CLAWMETER_MINISIGN_KEY": "k"},
		{"CLAWMETER_MINISIGN_PASSWORD": "p"},
	} {
		err := run([]string{"sign", file, "v1.0.0"}, func(k string) string { return env[k] })
		if err == nil || !strings.Contains(err.Error(), "must both be set") {
			t.Fatalf("env %v: err = %v", env, err)
		}
		if _, statErr := os.Stat(file + ".minisig"); statErr == nil {
			t.Fatal("signature written without secrets")
		}
	}
}
