package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReview039DelayedApplyCannotDowngrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix executable fixtures")
	}
	api, _ := newFakeGitHub(t, "v0.40.0")
	rel, err := checkWith(context.Background(), "v0.39.0", api, defaultDLPrefix,
		&http.Client{Transport: &http.Transport{}})
	if err != nil || rel == nil {
		t.Fatalf("check: %v %v", rel, err)
	}
	// A second updater installs v0.41.0 before the old tray applies its pending v0.40.0.
	exe := filepath.Join(t.TempDir(), "clawmeter")
	current := "#!/bin/sh\necho v0.41.0\n"
	older := "#!/bin/sh\necho v0.40.0\n"
	if err := os.WriteFile(exe, []byte(current), 0755); err != nil {
		t.Fatal(err)
	}
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
		body := older
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt") {
			body = fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(older)), assetNameFor(runtime.GOOS, runtime.GOARCH))
		}
		return fixtureResponse(r, 200, body), nil
	})
	err = ApplyTo(context.Background(), rel.URL, exe)
	got, readErr := os.ReadFile(exe)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != current {
		t.Fatalf("delayed apply downgraded installed v0.41.0: err=%v installed=%q", err, got)
	}
}
