// Refresh the updater's trust snapshot through Sigstore's authenticated TUF chain.
package main

import (
	"log"
	"os"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

func main() {
	opts := tuf.DefaultOptions()
	opts.DisableLocalCache = true
	client, err := tuf.New(opts)
	if err != nil {
		log.Fatal(err)
	}
	data, err := client.GetTarget("trusted_root.json")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := root.NewTrustedRootFromJSON(data); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("internal/update/trusted_root.json", data, 0644); err != nil {
		log.Fatal(err)
	}
}
