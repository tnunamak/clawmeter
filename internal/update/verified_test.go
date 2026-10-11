package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These supplemental tests require the new runSmoke, renameFile and copyArtifact
// hooks. security_test.go is independently runnable on the unmodified base.
func stubUpdate(t *testing.T, sums string) (exe string, runs, renames *int) {
	t.Helper()
	oldTransport, oldRun, oldRename, oldCopy := http.DefaultTransport, runSmoke, renameFile, copyArtifact
	t.Cleanup(func() {
		http.DefaultTransport, runSmoke, renameFile, copyArtifact = oldTransport, oldRun, oldRename, oldCopy
	})
	dir := t.TempDir()
	exe = filepath.Join(dir, "clawmeter")
	if err := os.WriteFile(exe, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	runs, renames = new(int), new(int)
	http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
		data := "verified fixture, never executed"
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt") {
			data = sums
		}
		return fixtureResponse(r, 200, data), nil
	})
	runSmoke = func(ctx context.Context, staged string) error {
		*runs++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) <= 0 {
			t.Errorf("smoke runner missing bounded deadline")
		}
		if filepath.Dir(staged) != dir || staged == exe {
			t.Errorf("binary not uniquely staged in destination directory: %s", staged)
		}
		got, err := os.ReadFile(staged)
		if err != nil || string(got) != "verified fixture, never executed" {
			t.Errorf("unexpected staged data: %q %v", got, err)
		}
		if err := verifyArtifact(staged, "clawmeter-linux-amd64", []byte(validFixtureSums())); err != nil {
			t.Errorf("runner reached unverified data: %v", err)
		}
		return nil
	}
	renameFile = func(from, to string) error {
		*renames++
		return os.Rename(from, to)
	}
	return
}

func fixtureResponse(r *http.Request, status int, data string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data)), Request: r}
}

func validFixtureSums() string {
	return fmt.Sprintf("%x  clawmeter-linux-amd64\n", sha256.Sum256([]byte("verified fixture, never executed")))
}

func assertOriginal(t *testing.T, exe string) {
	t.Helper()
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "original" {
		t.Fatalf("original damaged: %q %v", got, err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(exe), ".clawmeter-update-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("staging files leaked: %v %v", files, err)
	}
}

func TestApplyIntegrityGate(t *testing.T) {
	for _, tc := range []struct{ name, sums string }{
		{"missing entry", ""},
		{"wrong filename", strings.ReplaceAll(validFixtureSums(), "amd64", "arm64")},
		{"duplicate", validFixtureSums() + validFixtureSums()},
		{"mismatch", strings.Repeat("0", 64) + "  clawmeter-linux-amd64\n"},
		{"invalid hash", "xyz  clawmeter-linux-amd64\n"},
		{"malformed duplicate", validFixtureSums() + strings.TrimSpace(validFixtureSums()) + " extra\n"},
		{"duplicate binary mode", validFixtureSums() + strings.Replace(validFixtureSums(), "  ", " *", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, runs, renames := stubUpdate(t, tc.sums)
			err := ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe)
			if err == nil || !strings.Contains(err.Error(), "checksums") {
				t.Fatalf("expected checksum refusal, got %v", err)
			}
			if *runs != 0 || *renames != 0 {
				t.Fatalf("unsafe side effect: executions=%d renames=%d", *runs, *renames)
			}
			assertOriginal(t, exe)
		})
	}
}

func TestApplyRejectsRedirectBeforeTransport(t *testing.T) {
	for _, target := range []string{
		"https://evil.example/payload",
		"http://release-assets.githubusercontent.com/payload",
		"https://user@github.com/payload",
		"https://objects.githubusercontent.com.evil.example/payload",
		"https://release-assets.githubusercontent.com:444/payload",
	} {
		for _, resource := range []string{"sums", "binary"} {
			t.Run(resource+"_"+target, func(t *testing.T) {
				exe, runs, renames := stubUpdate(t, validFixtureSums())
				base := http.DefaultTransport
				forbiddenRequests := 0
				http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.String() == target {
						forbiddenRequests++
						return nil, errors.New("forbidden transport reached")
					}
					isSums := strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt")
					if (resource == "sums") == isSums {
						res := fixtureResponse(r, 302, "")
						res.Header.Set("Location", target)
						return res, nil
					}
					return base.RoundTrip(r)
				})
				if err := ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe); err == nil {
					t.Fatal("forbidden redirect accepted")
				}
				if forbiddenRequests != 0 || *runs != 0 || *renames != 0 {
					t.Fatalf("unsafe redirect side effects: requests=%d executions=%d renames=%d", forbiddenRequests, *runs, *renames)
				}
				assertOriginal(t, exe)
			})
		}
	}
}

func TestApplyVerifiedNewerArtifact(t *testing.T) {
	for _, host := range []string{"github.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com", "release-assets.githubusercontent.com"} {
		t.Run(host, func(t *testing.T) {
			exe, runs, renames := stubUpdate(t, validFixtureSums())
			base := http.DefaultTransport
			http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/releases/download/") {
					res := fixtureResponse(r, 302, "")
					res.Header.Set("Location", "https://"+host+"/fixture/"+filepath.Base(r.URL.Path))
					return res, nil
				}
				return base.RoundTrip(r)
			})
			api, _ := newFakeGitHubWithAssets(t, "v2.0.0", map[string]string{
				assetNameFor(runtime.GOOS, runtime.GOARCH): defaultDLPrefix + "/v2.0.0/clawmeter-linux-amd64",
			})
			// The fixture API uses a separate client so only downloads use the stub.
			rel, err := checkWith(context.Background(), "v1.0.0", api, defaultDLPrefix, &http.Client{Transport: &http.Transport{}})
			if err != nil || rel == nil {
				t.Fatalf("check: %v %v", rel, err)
			}
			if err := ApplyTo(context.Background(), rel.URL, exe); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(exe)
			if err != nil || string(got) != "verified fixture, never executed" || *runs != 1 || *renames == 0 {
				t.Fatalf("install failed: %q %v executions=%d renames=%d", got, err, *runs, *renames)
			}
		})
	}
}

func TestApplyFailurePreservesOriginal(t *testing.T) {
	for _, failure := range []string{"copy", "rename", "smoke", "cancelled smoke"} {
		t.Run(failure, func(t *testing.T) {
			exe, runs, _ := stubUpdate(t, validFixtureSums())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "copy":
				copyArtifact = func(dst io.Writer, src io.Reader) (int64, error) {
					_, _ = dst.Write([]byte("partial"))
					return 7, errors.New("injected write failure")
				}
			case "rename":
				renameFile = func(string, string) error { return errors.New("injected rename failure") }
			case "smoke":
				runSmoke = func(context.Context, string) error { return errors.New("injected smoke failure") }
			case "cancelled smoke":
				runSmoke = func(smoke context.Context, _ string) error {
					cancel()
					<-smoke.Done()
					return nil
				}
			}
			if err := ApplyTo(ctx, defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe); err == nil {
				t.Fatal("injected failure ignored")
			}
			if failure == "copy" && *runs != 0 {
				t.Fatal("partial download executed")
			}
			assertOriginal(t, exe)
		})
	}
}

func TestNoInstallForOldEqualMalformedTags(t *testing.T) {
	for _, tag := range []string{"v0.9.0", "v1.0.0", "1.0.0+build", "v1.0", "v2.0.0-01"} {
		t.Run(tag, func(t *testing.T) {
			exe, runs, renames := stubUpdate(t, validFixtureSums())
			api, dl := newFakeGitHub(t, tag)
			rel, err := checkWith(context.Background(), "v1.0.0", api, dl, &http.Client{Transport: &http.Transport{}})
			if err == nil && rel != nil {
				_ = ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe)
			}
			if rel != nil || *runs != 0 || *renames != 0 {
				t.Fatalf("tag %q reached installation", tag)
			}
			assertOriginal(t, exe)
		})
	}
}

func TestApplyEarlyRefusalHasNoSideEffects(t *testing.T) {
	for _, raw := range []string{
		"http://github.com/tnunamak/clawmeter/releases/download/v2.0.0/clawmeter-linux-amd64",
		"https://foreign.example/clawmeter",
		"https://user@github.com/tnunamak/clawmeter/releases/download/v2.0.0/clawmeter-linux-amd64",
		defaultDLPrefix + "/malformed/clawmeter-linux-amd64",
		defaultDLPrefix + "/v2.0.0/clawmeter-linux-amd64",
	} {
		t.Run(raw, func(t *testing.T) {
			exe, runs, renames := stubUpdate(t, validFixtureSums())
			http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt") {
					t.Error("binary requested before missing checksums were rejected")
				}
				return fixtureResponse(r, 404, ""), nil
			})
			if err := ApplyTo(context.Background(), raw, exe); err == nil {
				t.Fatal("unsafe or missing resource accepted")
			}
			if *runs != 0 || *renames != 0 {
				t.Fatalf("unsafe side effects: executions=%d renames=%d", *runs, *renames)
			}
			assertOriginal(t, exe)
		})
	}
}
