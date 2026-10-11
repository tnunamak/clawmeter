package update

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckStrictlyNewerSemver(t *testing.T) {
	for _, tc := range []struct {
		current, tag string
		newer        bool
	}{
		{"v2.0.0", "v1.9.9", false},
		{"1.2.3", "v1.2.3", false},
		{"v1.2.3+one", "v1.2.3+two", false},
		{"v1.2.3", "v1.2.3-rc.1", false},
		{"v1.2.3-rc.10", "v1.2.3-rc.2", false},
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3-rc.1", "v1.2.3", true},
		{"v1.2.3-rc.2", "v1.2.3-rc.10", true},
		{"v1.2.3", "v01.3.0", false},
		{"v1.2.3", "v2.0", false},
		{"v1.2.3", "v2.0.0-01", false},
		{"v1.2.3", "v2.0.0+", false},
		{"v1.2.3", "../v9.0.0", false},
	} {
		t.Run(tc.current+"_"+tc.tag, func(t *testing.T) {
			api, dl := newFakeGitHub(t, tc.tag)
			rel, err := checkWith(context.Background(), tc.current, api, dl, http.DefaultClient)
			if tc.newer && (err != nil || rel == nil) {
				t.Fatalf("newer valid tag rejected: %v, %v", rel, err)
			}
			if !tc.newer && rel != nil {
				t.Fatalf("unsafe update accepted: %+v", rel)
			}
		})
	}
}

func TestApplyRejectsUntrustedURLBeforeRequest(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	for _, raw := range []string{
		"http://github.com/tnunamak/clawmeter/releases/download/v2.0.0/clawmeter-linux-amd64",
		"https://evil.example/clawmeter",
		"https://github.com.evil.example/clawmeter",
		"https://user@github.com/tnunamak/clawmeter/releases/download/v2.0.0/clawmeter-linux-amd64",
		"https://github.com:444/tnunamak/clawmeter/releases/download/v2.0.0/clawmeter-linux-amd64",
	} {
		t.Run(raw, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = updateTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("stub: never download or execute")
			})
			exe := filepath.Join(t.TempDir(), "clawmeter")
			if err := os.WriteFile(exe, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ApplyTo(context.Background(), raw, exe); err == nil {
				t.Fatal("untrusted URL accepted")
			}
			if calls != 0 {
				t.Fatalf("untrusted URL reached transport %d times", calls)
			}
			got, err := os.ReadFile(exe)
			if err != nil || string(got) != "original" {
				t.Fatalf("original changed: %q %v", got, err)
			}
		})
	}
}

func TestApplyMissingSumsBeforeBinaryDownload(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	sums, binaries := 0, 0
	http.DefaultTransport = updateTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt") {
			sums++
			return &http.Response{StatusCode: 404, Body: http.NoBody, Header: make(http.Header), Request: r}, nil
		}
		binaries++
		return nil, errors.New("stub: never download or execute")
	})
	exe := filepath.Join(t.TempDir(), "clawmeter")
	if err := os.WriteFile(exe, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	err := ApplyTo(context.Background(), defaultDLPrefix+"/v2.0.0/clawmeter-linux-amd64", exe)
	if err == nil {
		t.Fatal("missing sums accepted")
	}
	if sums != 1 || binaries != 0 {
		t.Fatalf("missing sums must abort before binary download: sums=%d binaries=%d", sums, binaries)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "original" {
		t.Fatalf("original changed: %q %v", got, err)
	}
}
