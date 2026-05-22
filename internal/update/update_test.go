package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

// newFakeGitHub returns a test server that responds with the given tag_name
// and a download-prefix to substitute for the real github.com URL.
func newFakeGitHub(t *testing.T, tag string) (api, dl string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, srv.URL + "/download"
}

func newFakeGitHubWithAssets(t *testing.T, tag string, assets map[string]string) (api, dl string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[`, tag)
		first := true
		for name, url := range assets {
			if !first {
				fmt.Fprint(w, ",")
			}
			first = false
			fmt.Fprintf(w, `{"name":%q,"browser_download_url":%q}`, name, url)
		}
		fmt.Fprint(w, `]}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, srv.URL + "/download"
}

func TestCheck_findsUpdate(t *testing.T) {
	api, dl := newFakeGitHub(t, "v9.9.9")
	rel, err := checkWith(context.Background(), "v0.0.1", api, dl, http.DefaultClient)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel == nil {
		t.Fatal("expected update, got nil")
	}
	if rel.Version != "v9.9.9" {
		t.Fatalf("got version %q, want v9.9.9", rel.Version)
	}
	wantSuffix := fmt.Sprintf("clawmeter-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		wantSuffix += ".exe"
	}
	if !strings.HasSuffix(rel.URL, wantSuffix) {
		t.Fatalf("URL %q does not end with %q", rel.URL, wantSuffix)
	}
	if !strings.Contains(rel.URL, "/v9.9.9/") {
		t.Fatalf("URL %q does not contain tag", rel.URL)
	}
}

func TestCheck_usesReleaseAssetURL(t *testing.T) {
	asset := assetNameFor(runtime.GOOS, runtime.GOARCH)
	wantURL := "https://download.example/clawmeter"
	api, dl := newFakeGitHubWithAssets(t, "v9.9.9", map[string]string{
		asset: wantURL,
	})
	rel, err := checkWith(context.Background(), "v0.0.1", api, dl, http.DefaultClient)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel == nil {
		t.Fatal("expected update, got nil")
	}
	if rel.URL != wantURL {
		t.Fatalf("URL = %q, want %q", rel.URL, wantURL)
	}
}

func TestCheck_waitsForCurrentPlatformAsset(t *testing.T) {
	api, dl := newFakeGitHubWithAssets(t, "v9.9.9", map[string]string{
		"clawmeter-plan9-mips": "https://download.example/wrong",
	})
	_, err := checkWith(context.Background(), "v0.0.1", api, dl, http.DefaultClient)
	if err == nil {
		t.Fatal("expected error for missing current-platform asset")
	}
	if !strings.Contains(err.Error(), assetNameFor(runtime.GOOS, runtime.GOARCH)) {
		t.Fatalf("error %q does not mention missing asset", err)
	}
}

func TestCheck_devSkipped(t *testing.T) {
	api, dl := newFakeGitHub(t, "v9.9.9")
	rel, err := checkWith(context.Background(), "dev", api, dl, http.DefaultClient)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel != nil {
		t.Fatalf("expected nil for dev, got %+v", rel)
	}
}

func TestCheck_currentVersionUpToDate(t *testing.T) {
	api, dl := newFakeGitHub(t, "v1.2.3")
	rel, err := checkWith(context.Background(), "v1.2.3", api, dl, http.DefaultClient)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel != nil {
		t.Fatalf("expected nil for current version, got %+v", rel)
	}
}

func TestCheck_emptyTag(t *testing.T) {
	api, dl := newFakeGitHub(t, "")
	rel, err := checkWith(context.Background(), "v0.0.1", api, dl, http.DefaultClient)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel != nil {
		t.Fatalf("expected nil for empty tag, got %+v", rel)
	}
}

func TestCheck_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := checkWith(context.Background(), "v0.0.1", srv.URL, srv.URL, http.DefaultClient)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestParseRestartHelperArgs(t *testing.T) {
	parentPID, exe, err := parseRestartHelperArgs([]string{"--parent-pid", "123", "--exe", "/tmp/clawmeter"})
	if err != nil {
		t.Fatalf("parseRestartHelperArgs error: %v", err)
	}
	if parentPID != 123 {
		t.Fatalf("parentPID = %d, want 123", parentPID)
	}
	if exe != "/tmp/clawmeter" {
		t.Fatalf("exe = %q, want /tmp/clawmeter", exe)
	}
}

func TestParseRestartHelperArgsRejectsBadInput(t *testing.T) {
	tests := [][]string{
		{"--parent-pid"},
		{"--parent-pid", "nope"},
		{"--exe"},
		{"--unknown"},
	}
	for _, tt := range tests {
		if _, _, err := parseRestartHelperArgs(tt); err == nil {
			t.Fatalf("parseRestartHelperArgs(%v) expected error", tt)
		}
	}
}

// TestCheck_live is gated behind CLAWMETER_LIVE_UPDATE_CHECK=1 so normal
// unit runs don't hit the real GitHub API.
func TestCheck_live(t *testing.T) {
	if os.Getenv("CLAWMETER_LIVE_UPDATE_CHECK") != "1" {
		t.Skip("set CLAWMETER_LIVE_UPDATE_CHECK=1 to run live GitHub check")
	}
	rel, err := Check(context.Background(), "v0.0.1")
	if err != nil {
		t.Fatalf("live Check error: %v", err)
	}
	if rel == nil {
		t.Fatal("expected an update from live API")
	}
	t.Logf("live update: %s -> %s", rel.Version, rel.URL)
}
