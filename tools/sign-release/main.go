// Command sign-release signs and verifies a release's SHA256SUMS.txt with
// minisign. It is a CI tool and is not part of the clawmeter binary.
//
//	sign-release sign   <file> <tag>   writes <file>.minisig
//	sign-release verify <file> <tag>   checks <file>.minisig against the embedded keys
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"aead.dev/minisign"
	"github.com/tnunamak/clawmeter/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "sign-release: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string) error {
	if len(args) != 3 {
		return errors.New("usage: sign-release sign|verify <file> <tag>")
	}
	mode, file, tag := args[0], args[1], args[2]
	switch mode {
	case "sign":
		return sign(file, tag, getenv)
	case "verify":
		return verify(file, tag)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func sign(file, tag string, getenv func(string) string) error {
	keyText, password := getenv("CLAWMETER_MINISIGN_KEY"), getenv("CLAWMETER_MINISIGN_PASSWORD")
	if keyText == "" || password == "" {
		return errors.New("CLAWMETER_MINISIGN_KEY and CLAWMETER_MINISIGN_PASSWORD must both be set")
	}
	key, err := minisign.DecryptKey(password, []byte(keyText))
	if err != nil {
		return fmt.Errorf("decrypt key: %w", err)
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	// Prehashed (ED) signatures are the minisign CLI default; every CLI
	// version verifies them.
	reader := minisign.NewReader(f)
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	untrusted := fmt.Sprintf("signature from private key: %016X", key.ID())
	sig := reader.SignWithComments(key, update.TrustedComment(tag), untrusted)
	return os.WriteFile(file+".minisig", sig, 0o644)
}

// verifySums is the updater's verifier; tests may swap in a test key set.
var verifySums = update.VerifySums

func verify(file, tag string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(file + ".minisig")
	if err != nil {
		return err
	}
	return verifySums(data, sig, tag)
}
