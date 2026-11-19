package opencodego

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakePassword = "s3cret-fake-password"

// newTestProvider writes a fake service.json pointing at url and returns a
// provider that reads it.
func newTestProvider(t *testing.T, url string) *Provider {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service.json")
	body := `{"url":"` + url + `","password":"` + fakePassword + `","pid":1,"id":"x","version":"2.3.2"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.serviceFile = path
	p.homeDir = func() (string, error) { return "/home/fake", nil }
	return p
}

func serve(t *testing.T, status int, contentType, body string) (*Provider, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(context.Background())
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	p := newTestProvider(t, srv.URL)
	// the request is captured after FetchUsage returns; callers read &got lazily
	return p, &got
}

func fetchErr(t *testing.T, p *Provider) string {
	t.Helper()
	data, err := p.FetchUsage(context.Background())
	if err == nil {
		t.Fatalf("expected error, got data %+v", data)
	}
	if data != nil {
		t.Fatalf("expected no data with error, got %+v", data)
	}
	if strings.Contains(err.Error(), fakePassword) {
		t.Fatalf("error leaks password: %v", err)
	}
	return err.Error()
}

func TestFetchUsageSuccess(t *testing.T) {
	const body = `{"location":{"directory":"/home/fake"},"data":{"workspace":{"id":"w","name":"n","usageURL":"u"},
	"go":{"rolling":{"percent":42.5,"limited":false,"resetsAt":1800000000000},
	"weekly":{"percent":10,"limited":true,"resetsAt":1800100000000},
	"monthly":{"percent":150,"limited":false,"resetsAt":1800200000000}},"budgets":[{"x":1}]}}`
	p, req := serve(t, 200, "application/json", body)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.Provider != "opencode_go" || len(data.Windows) != 3 {
		t.Fatalf("unexpected data: %+v", data)
	}
	// The monthly window has no published length, so it claims none.
	want := []struct {
		name, display string
		util          float64
		length        time.Duration
		reset         int64
	}{
		{"5h", "5 hours", 42.5, 5 * time.Hour, 1800000000000},
		{"7d", "7 days", 100, 168 * time.Hour, 1800100000000},
		{"monthly", "Monthly", 100, 0, 1800200000000},
	}
	for i, w := range want {
		g := data.Windows[i]
		if g.Name != w.name || g.DisplayName != w.display || g.Utilization != w.util || g.Length != w.length || g.LengthUnknown != (w.length == 0) || !g.ResetsAt.Equal(time.UnixMilli(w.reset)) {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
	if req.URL.Path != "/api/experimental/usage/limits" || req.URL.Query().Get("location[directory]") != "/home/fake" {
		t.Errorf("unexpected request URL %v", req.URL)
	}
	if user, pass, ok := req.BasicAuth(); !ok || user != "opencode" || pass != fakePassword {
		t.Errorf("bad basic auth: %q %q %v", user, pass, ok)
	}
}

func TestFetchUsageClampsPercent(t *testing.T) {
	const body = `{"data":{"go":{"rolling":{"percent":150,"limited":false,"resetsAt":1},
	"weekly":{"percent":-3,"limited":false,"resetsAt":1}}}}`
	p, _ := serve(t, 200, "application/json", body)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 2 || data.Windows[0].Utilization != 100 || data.Windows[1].Utilization != 0 {
		t.Fatalf("unexpected windows: %+v", data.Windows)
	}
}

func TestNotSignedIn(t *testing.T) {
	p, _ := serve(t, 200, "application/json", `{"location":{"directory":"/"},"data":null}`)
	msg := fetchErr(t, p)
	if !strings.Contains(msg, "opencode auth login opencode --method device") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestUnauthorizedDoesNotLeakPassword(t *testing.T) {
	p, _ := serve(t, 401, "text/plain", "bad password "+fakePassword)
	msg := fetchErr(t, p)
	if !strings.Contains(msg, "rejected") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestNoUsageEndpoint(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		ct     string
	}{
		"404":  {404, "application/json"},
		"405":  {405, "application/json"},
		"html": {200, "text/html; charset=utf-8"},
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := serve(t, tc.status, tc.ct, "<html></html>")
			if msg := fetchErr(t, p); !strings.Contains(msg, "no usage endpoint") {
				t.Fatalf("unexpected message: %s", msg)
			}
		})
	}
}

func TestMissingServiceFile(t *testing.T) {
	p := New()
	p.serviceFile = filepath.Join(t.TempDir(), "nope", "service.json")
	if p.IsConfigured() {
		t.Fatal("IsConfigured must be false without service.json")
	}
	fetchErr(t, p)
}

func TestIsConfiguredWithServiceFile(t *testing.T) {
	// No server is listening: IsConfigured must not touch the network.
	p := newTestProvider(t, "http://127.0.0.1:1")
	if !p.IsConfigured() {
		t.Fatal("IsConfigured should be true when service.json exists")
	}
}

func TestSetupStatusNamesTheMissingStep(t *testing.T) {
	p := New()
	p.serviceFile = filepath.Join(t.TempDir(), "service.json")
	if st := p.SetupStatus(); st.IsReady() || !strings.Contains(st.Detail, "opencode serve --service") {
		t.Fatalf("missing service file: %+v", st)
	}
	if st := newTestProvider(t, "http://127.0.0.1:1").SetupStatus(); !st.IsReady() {
		t.Fatalf("service file present: %+v", st)
	}
}

func TestDefaultServicePath(t *testing.T) {
	p := New()
	p.homeDir = func() (string, error) { return "/h", nil }
	t.Setenv("XDG_STATE_HOME", "")
	if got, _ := p.servicePath(); got != filepath.Join("/h", ".local", "state", "opencode", "service.json") {
		t.Errorf("default path = %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "/x")
	if got, _ := p.servicePath(); got != filepath.Join("/x", "opencode", "service.json") {
		t.Errorf("xdg path = %s", got)
	}
}

func TestServiceNotRunning(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	p := newTestProvider(t, "http://"+addr)
	if msg := fetchErr(t, p); !strings.Contains(msg, "not running") || !strings.Contains(msg, "opencode serve --service") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestServiceDoesNotAnswerInTime(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	// Cleanups run last-in first-out, so the handler is released before the server closes.
	t.Cleanup(func() { close(release) })
	p := newTestProvider(t, srv.URL)
	p.client.Timeout = 50 * time.Millisecond
	if msg := fetchErr(t, p); !strings.Contains(msg, "did not answer in time") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestMalformedAndOversizedBodies(t *testing.T) {
	p, _ := serve(t, 200, "application/json", `{"data":`)
	if msg := fetchErr(t, p); !strings.Contains(msg, "malformed") {
		t.Fatalf("unexpected message: %s", msg)
	}
	huge := `{"data":null,"pad":"` + strings.Repeat("a", 1<<20) + `"}`
	p, _ = serve(t, 200, "application/json", huge)
	if msg := fetchErr(t, p); !strings.Contains(msg, "too large") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestMissingGoUsage(t *testing.T) {
	p, _ := serve(t, 200, "application/json", `{"data":{"workspace":{"id":"w"},"budgets":[]}}`)
	if msg := fetchErr(t, p); !strings.Contains(msg, "missing Go plan usage") {
		t.Fatalf("unexpected message: %s", msg)
	}
	p, _ = serve(t, 200, "application/json", `{"data":{"go":{}}}`)
	fetchErr(t, p)
}

func TestWindowMissingFieldsIsError(t *testing.T) {
	p, _ := serve(t, 200, "application/json", `{"data":{"go":{"rolling":{"limited":false,"resetsAt":1}}}}`)
	if msg := fetchErr(t, p); !strings.Contains(msg, "malformed") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestIdentity(t *testing.T) {
	p := New()
	if p.Name() != "opencode_go" || p.DisplayName() != "OpenCode Go" || p.Description() == "" || p.DashboardURL() != "https://opencode.ai/auth" {
		t.Fatal("unexpected identity")
	}
}
