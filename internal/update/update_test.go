package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestCheck_bindsDownloadToCheckedTag(t *testing.T) {
	asset := assetNameFor(runtime.GOOS, runtime.GOARCH)
	api, dl := newFakeGitHubWithAssets(t, "v9.9.9", map[string]string{
		asset: "https://download.example/clawmeter",
	})
	rel, err := checkWith(context.Background(), "v0.0.1", api, dl, http.DefaultClient)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel == nil {
		t.Fatal("expected update, got nil")
	}
	// The metadata's asset URL is ignored; the URL comes from the checked tag.
	if want := strings.TrimRight(dl, "/") + "/v9.9.9/" + asset; rel.URL != want {
		t.Fatalf("URL = %q, want %q", rel.URL, want)
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

func TestCheck_rateLimitedAPIFallsBackToTheReleasePage(t *testing.T) {
	for name, tc := range map[string]struct {
		api      int
		location string
		want     string
	}{
		"403 then tag":        {http.StatusForbidden, "/" + repo + "/releases/tag/v9.9.9", "v9.9.9"},
		"429 then tag":        {http.StatusTooManyRequests, "/" + repo + "/releases/tag/v9.9.9", "v9.9.9"},
		"no redirect":         {http.StatusForbidden, "", ""},
		"another repository":  {http.StatusForbidden, "/someone/else/releases/tag/v9.9.9", ""},
		"not a version":       {http.StatusForbidden, "/" + repo + "/releases/tag/nightly", ""},
		"a path in the tag":   {http.StatusForbidden, "/" + repo + "/releases/tag/v9.9.9/x", ""},
		"other errors do not": {http.StatusBadGateway, "/" + repo + "/releases/tag/v9.9.9", ""},
		"another host":        {http.StatusForbidden, "https://example.com/" + repo + "/releases/tag/v9.9.9", ""},
		"an encoded path":     {http.StatusForbidden, "/" + repo + "/releases/tag/v9.9.9%2F..%2Fx", ""},
		"a query is ignored":  {http.StatusForbidden, "/" + repo + "/releases/tag/v9.9.9?from=x", "v9.9.9"},
		"the current tag":     {http.StatusForbidden, "/" + repo + "/releases/tag/v0.0.1", "none"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api" {
					w.WriteHeader(tc.api)
					return
				}
				if r.Method != http.MethodHead || r.URL.Path != "/latest" {
					t.Errorf("page request = %s %s", r.Method, r.URL.Path)
				}
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
					w.WriteHeader(http.StatusFound)
				}
			}))
			defer srv.Close()
			saved := latestURL
			latestURL = srv.URL + "/latest"
			defer func() { latestURL = saved }()
			rel, err := checkWith(context.Background(), "v0.0.1", srv.URL+"/api", srv.URL+"/download", http.DefaultClient)
			if tc.want == "none" {
				if err != nil || rel != nil {
					t.Fatalf("same version = %+v, %v", rel, err)
				}
				return
			}
			if tc.want == "" {
				if err == nil {
					t.Fatalf("expected an error, got %+v", rel)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if rel == nil || rel.Version != tc.want || rel.URL != srv.URL+"/download/"+tc.want+"/"+assetNameFor(runtime.GOOS, runtime.GOARCH) {
				t.Fatalf("release = %+v", rel)
			}
		})
	}
}

func TestLatestTagNeedsARedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/"+repo+"/releases/tag/v9.9.9")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if tag, err := latestTag(context.Background(), srv.URL, http.DefaultClient); err == nil {
		t.Fatalf("a 200 with a Location gave tag %q", tag)
	}
}

const homebrewReceiptFixture = `{"homebrew_version":"4.4.0","source":{"path":"/test/Homebrew/Library/Taps/tnunamak/homebrew-tap/Formula/clawmeter.rb"}}`

func writeHomebrewFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func homebrewFixture(t *testing.T, prefix string) (string, string) {
	t.Helper()
	keg := filepath.Join(prefix, "Cellar", "clawmeter", "1.2.3_1")
	exe := filepath.Join(keg, "bin", "clawmeter")
	receipt := filepath.Join(keg, "INSTALL_RECEIPT.json")
	writeHomebrewFixtureFile(t, exe, "installed binary")
	writeHomebrewFixtureFile(t, receipt, homebrewReceiptFixture)
	return exe, receipt
}

func homebrewFixtureSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func TestHomebrewOriginPrefixesAndSymlinks(t *testing.T) {
	for _, prefix := range []string{"opt/homebrew", "usr/local", "home/linuxbrew/.linuxbrew", "custom prefix/brew"} {
		t.Run(prefix, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), filepath.FromSlash(prefix))
			exe, _ := homebrewFixture(t, root)
			binLink := filepath.Join(root, "bin", "clawmeter")
			homebrewFixtureSymlink(t, filepath.Join("..", "Cellar", "clawmeter", "1.2.3_1", "bin", "clawmeter"), binLink)
			opt := filepath.Join(root, "opt", "clawmeter")
			homebrewFixtureSymlink(t, filepath.Join("..", "Cellar", "clawmeter", "1.2.3_1"), opt)
			alias := filepath.Join(t.TempDir(), "renamed-command")
			homebrewFixtureSymlink(t, filepath.Join(opt, "bin", "clawmeter"), alias)
			for _, path := range []string{exe, binLink, filepath.Join(opt, "bin", "clawmeter"), alias} {
				if err := checkHomebrewOrigin(path); !errors.Is(err, ErrHomebrewManaged) {
					t.Errorf("origin %q = %v, want Homebrew guard", path, err)
				}
			}
		})
	}
}

func TestHomebrewOriginNeedsPackageEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, path, receipt string
	}{
		{"standalone", "usr/local/bin/clawmeter", homebrewReceiptFixture},
		{"loose Cellar substring", "myCellar/clawmeter/1.2.3/bin/clawmeter", homebrewReceiptFixture},
		{"other formula", "Cellar/other/1.2.3/bin/clawmeter", homebrewReceiptFixture},
		{"unversioned", "Cellar/clawmeter/bin/clawmeter", homebrewReceiptFixture},
		{"nested binary", "Cellar/clawmeter/1.2.3/libexec/bin/clawmeter", homebrewReceiptFixture},
		{"no receipt", "Cellar/clawmeter/1.2.3/bin/clawmeter", ""},
		{"empty object", "Cellar/clawmeter/1.2.3/bin/clawmeter", "{}"},
		{"malformed receipt", "Cellar/clawmeter/1.2.3/bin/clawmeter", "{"},
		{"other formula receipt", "Cellar/clawmeter/1.2.3/bin/clawmeter", `{"homebrew_version":"4.4.0","source":{"path":"/Formula/other.rb"}}`},
		{"no Homebrew version", "Cellar/clawmeter/1.2.3/bin/clawmeter", `{"source":{"path":"/Formula/clawmeter.rb"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), filepath.FromSlash(tc.path))
			writeHomebrewFixtureFile(t, exe, "standalone")
			if tc.receipt != "" {
				writeHomebrewFixtureFile(t, filepath.Join(filepath.Dir(filepath.Dir(exe)), "INSTALL_RECEIPT.json"), tc.receipt)
			}
			if err := checkHomebrewOrigin(exe); err != nil {
				t.Fatalf("standalone origin = %v", err)
			}
		})
	}
}

func TestHomebrewOriginCopyAndResolvedTarget(t *testing.T) {
	root := t.TempDir()
	managed, _ := homebrewFixture(t, filepath.Join(root, "brew"))
	copyPath := filepath.Join(root, "standalone", "clawmeter")
	body, err := os.ReadFile(managed)
	if err != nil {
		t.Fatal(err)
	}
	writeHomebrewFixtureFile(t, copyPath, string(body))
	// Merely having brew on PATH must not change a standalone binary's origin.
	writeHomebrewFixtureFile(t, filepath.Join(root, "tools", "brew"), "not a command")
	t.Setenv("PATH", filepath.Join(root, "tools"))
	if err := checkHomebrewOrigin(copyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	homebrewFixtureSymlink(t, copyPath, managed)
	if err := checkHomebrewOrigin(managed); err != nil {
		t.Fatalf("symlink out of a keg is standalone: %v", err)
	}
}

func TestHomebrewOriginReceiptBoundsAndKinds(t *testing.T) {
	for _, kind := range []string{"oversized", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			exe, receipt := homebrewFixture(t, t.TempDir())
			if err := os.Remove(receipt); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "oversized":
				writeHomebrewFixtureFile(t, receipt, strings.Repeat(" ", (1<<20)+1))
			case "directory":
				if err := os.Mkdir(receipt, 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "receipt")
				writeHomebrewFixtureFile(t, target, homebrewReceiptFixture)
				homebrewFixtureSymlink(t, target, receipt)
			}
			err := checkHomebrewOrigin(exe)
			if kind == "oversized" {
				if err == nil || !strings.Contains(err.Error(), "size limit") {
					t.Fatalf("oversized receipt = %v", err)
				}
			} else if err != nil {
				t.Fatalf("nonregular receipt is not package evidence: %v", err)
			}
		})
	}
}

func TestHomebrewUpdateResolutionErrors(t *testing.T) {
	for _, kind := range []string{"dangling", "loop"} {
		t.Run(kind, func(t *testing.T) {
			link := filepath.Join(t.TempDir(), "clawmeter")
			target := link
			if kind == "dangling" {
				target += "-missing"
			}
			homebrewFixtureSymlink(t, target, link)
			saved := osExecutable
			osExecutable = func() (string, error) { return link, nil }
			defer func() { osExecutable = saved }()
			for _, call := range []func() error{
				CheckSelfUpdateAllowed,
				func() error { return Apply(context.Background(), "invalid URL") },
				func() error { return ApplyTo(context.Background(), "invalid URL", link) },
			} {
				if err := call(); err == nil || errors.Is(err, ErrHomebrewManaged) {
					t.Fatalf("resolution error = %v", err)
				}
			}
		})
	}
	saved := osExecutable
	osExecutable = func() (string, error) { return "", errors.New("executable unavailable") }
	defer func() { osExecutable = saved }()
	if err := CheckSelfUpdateAllowed(); err == nil || !strings.Contains(err.Error(), "executable unavailable") {
		t.Fatalf("executable failure = %v", err)
	}
}

type homebrewUpdateTransport func(*http.Request) (*http.Response, error)

func (f homebrewUpdateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestHomebrewUpdateEntryPointsHaveNoEffects(t *testing.T) {
	exe, receipt := homebrewFixture(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "clawmeter")
	homebrewFixtureSymlink(t, exe, link)
	writeHomebrewFixtureFile(t, exe+".old", "previous binary")
	savedExe, savedClient, savedTransport := osExecutable, httpClient, http.DefaultTransport
	osExecutable = func() (string, error) { return link, nil }
	requests := 0
	transport := homebrewUpdateTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("offline test transport")
	})
	httpClient = &http.Client{Transport: transport}
	http.DefaultTransport = transport
	defer func() {
		osExecutable, httpClient, http.DefaultTransport = savedExe, savedClient, savedTransport
	}()
	url := defaultDLPrefix + "/v9.9.9/" + assetNameFor(runtime.GOOS, runtime.GOARCH)
	for _, call := range []func() error{
		CheckSelfUpdateAllowed,
		func() error { return Apply(context.Background(), url) },
		func() error { return ApplyTo(context.Background(), url, exe) },
		func() error { return ApplyTo(context.Background(), url, link) },
	} {
		if err := call(); !errors.Is(err, ErrHomebrewManaged) || !strings.Contains(err.Error(), "brew upgrade clawmeter") {
			t.Fatalf("managed update = %v", err)
		}
	}
	CleanupOld()
	if requests != 0 {
		t.Fatalf("managed update made %d network requests", requests)
	}
	for path, want := range map[string]string{exe: "installed binary", exe + ".old": "previous binary", receipt: homebrewReceiptFixture} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("%s changed: %q, %v", path, data, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("unexpected staged artifacts: %v", entries)
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf("managed symlink replaced: %v", err)
	}
}

func TestHomebrewStandaloneUpdateStillReachesDownload(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "clawmeter")
	writeHomebrewFixtureFile(t, exe, "standalone")
	savedClient, savedTransport := httpClient, http.DefaultTransport
	requests := 0
	transport := homebrewUpdateTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("offline test transport")
	})
	httpClient = &http.Client{Transport: transport}
	http.DefaultTransport = transport
	defer func() { httpClient, http.DefaultTransport = savedClient, savedTransport }()
	url := defaultDLPrefix + "/v9.9.9/" + assetNameFor(runtime.GOOS, runtime.GOARCH)
	for _, path := range []string{exe, filepath.Join(filepath.Dir(exe), "new-destination")} {
		err := ApplyTo(context.Background(), url, path)
		if err == nil || !strings.Contains(err.Error(), "offline test transport") {
			t.Fatalf("standalone download = %v", err)
		}
	}
	if requests != 2 {
		t.Fatalf("standalone download requests = %d, want 2", requests)
	}
}
