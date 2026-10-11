package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A newer metadata tag must not authorize bytes and checksums from an older
// release. All HTTP and process execution are replaced with in-memory stubs.
func TestRedTeamNewerMetadataCannotAuthorizeRollbackAsset(t *testing.T) {
	for _, assetTag := range []string{"v1.0.0", "v2.0.0"} {
		t.Run(assetTag, func(t *testing.T) {
			oldTransport, oldSmoke, oldRename := http.DefaultTransport, runSmoke, renameFile
			t.Cleanup(func() {
				http.DefaultTransport, runSmoke, renameFile = oldTransport, oldSmoke, oldRename
			})
			asset := assetNameFor(runtime.GOOS, runtime.GOARCH)
			rawURL := defaultDLPrefix + "/" + assetTag + "/" + asset
			payload := []byte("fixture bytes from " + assetTag + "; never executed")
			sums := fmt.Sprintf("%x  %s\n", sha256.Sum256(payload), asset)
			metadata, err := json.Marshal(map[string]interface{}{
				"tag_name": "v3.0.0",
				"assets":   []map[string]string{{"name": asset, "browser_download_url": rawURL}},
			})
			if err != nil {
				t.Fatal(err)
			}
			requests, runs, replacements := 0, 0, 0
			http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				switch r.URL.String() {
				case defaultAPIURL:
					return fixtureResponse(r, http.StatusOK, string(metadata)), nil
				case defaultDLPrefix + "/" + assetTag + "/" + sumsName:
					return fixtureResponse(r, http.StatusOK, sums), nil
				case rawURL:
					return fixtureResponse(r, http.StatusOK, string(payload)), nil
				default:
					return nil, fmt.Errorf("unexpected fixture request: %s", r.URL)
				}
			})
			runSmoke = func(context.Context, string) error {
				runs++
				return nil
			}
			renameFile = func(from, to string) error {
				replacements++
				return os.Rename(from, to)
			}
			exe := filepath.Join(t.TempDir(), "clawmeter")
			if err := os.WriteFile(exe, []byte("installed v2.0.0"), 0700); err != nil {
				t.Fatal(err)
			}

			release, checkErr := checkWith(context.Background(), "v2.0.0",
				defaultAPIURL, defaultDLPrefix, &http.Client{})
			var applyErr error
			if checkErr == nil && release != nil {
				applyErr = ApplyTo(context.Background(), release.URL, exe)
			}
			got, readErr := os.ReadFile(exe)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if runs != 0 || replacements != 0 || string(got) != "installed v2.0.0" {
				t.Fatalf("newer metadata authorized non-newer asset %s: check=%v apply=%v requests=%d smoke=%d replacements=%d installed=%q",
					assetTag, checkErr, applyErr, requests, runs, replacements, got)
			}
			if checkErr == nil && release != nil && applyErr == nil {
				t.Fatal("mismatched release tag and asset URL were accepted")
			}
			if files, err := filepath.Glob(filepath.Join(filepath.Dir(exe), ".clawmeter-update-*")); err != nil || len(files) != 0 {
				t.Fatalf("staging files leaked: %s (%v)", strings.Join(files, ", "), err)
			}
		})
	}
}
