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
