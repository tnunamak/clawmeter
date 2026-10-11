// Use the updater's offline verification policy before publishing a release.
package main

import (
	"log"
	"os"

	"github.com/tnunamak/clawmeter/internal/update"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: verify-release SHA256SUMS.txt SHA256SUMS.txt.sigstore.json")
	}
	sums, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	bundle, err := os.ReadFile(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	if err := update.VerifyChecksumSignature(sums, bundle); err != nil {
		log.Fatal(err)
	}
}
