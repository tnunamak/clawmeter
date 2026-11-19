package opencode

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fakePassword = "s3cret-fake-password"
	fakeKey      = "fake-api-key-value"
)

// newTestProvider writes a fake service.json pointing at url and returns a
// provider that reads it. It clears OPENCODE_API_KEY so a key in the developer's
// shell cannot change the result.
func newTestProvider(t *testing.T, url string) *Provider {
	t.Helper()
	t.Setenv(apiKeyEnv, "")
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

// usageRoute is a fake OpenCode usage route. It counts requests and keeps the
// last request's headers, under a lock because the server runs in its own goroutine.
type usageRoute struct {
	mu   sync.Mutex
	hits int
	last http.Header
}

// serve starts the route and returns its URL.
func (u *usageRoute) serve(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.hits++
		u.last = r.Header.Clone()
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/zen/go/v1/usage"
}

func (u *usageRoute) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits
}

func (u *usageRoute) header(name string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.last.Get(name)
}

// newKeyProvider returns a provider whose service file is missing, so only the
// API key route can answer, and sets the key for the test.
func newKeyProvider(t *testing.T, key, usageURL string) *Provider {
	t.Helper()
	t.Setenv(apiKeyEnv, key)
	p := New()
	p.serviceFile = filepath.Join(t.TempDir(), "nope", "service.json")
	p.homeDir = func() (string, error) { return "/home/fake", nil }
	p.apiKeyURL = usageURL
	return p
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
	for _, secret := range []string{fakePassword, fakeKey} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks a secret: %v", err)
		}
	}
	return err.Error()
}

// keyBody is the usage route's shape: the same three windows as the service, with
// a status word and an RFC 3339 reset.
const keyBody = `{"usage":{` +
	`"rolling":{"status":"ok","percent":19,"resetsAt":"2026-11-19T15:53:47.000Z"},` +
	`"weekly":{"status":"rate-limited","percent":83,"resetsAt":"2026-11-25T00:00:00.000Z"},` +
	`"monthly":{"status":"ok","percent":41,"resetsAt":"2026-12-19T00:00:00.000Z"}}}`

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
	if data.Provider != "opencode" || len(data.Windows) != 3 {
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
		{"monthly", "1M", 100, 0, 1800200000000},
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

// OpenCode may omit a window from a response. The windows it sends are shown,
// and an omitted one is left out rather than shown as zero.
func TestPartialGoPlanShowsOnlyTheWindowsSent(t *testing.T) {
	const body = `{"data":{"go":{"rolling":{"percent":12,"limited":false,"resetsAt":1800000000000}}}}`
	p, _ := serve(t, 200, "application/json", body)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 1 || data.Windows[0].Name != "5h" || data.Windows[0].Utilization != 12 {
		t.Fatalf("windows = %+v, want only the 5-hour window", data.Windows)
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
	t.Setenv(apiKeyEnv, "")
	p := New()
	p.serviceFile = filepath.Join(t.TempDir(), "nope", "service.json")
	if p.IsConfigured() {
		t.Fatal("IsConfigured must be false without service.json or a key")
	}
	if msg := fetchErr(t, p); !strings.Contains(msg, "not found") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestIsConfiguredWithServiceFile(t *testing.T) {
	// No server is listening: IsConfigured must not touch the network.
	p := newTestProvider(t, "http://127.0.0.1:1")
	if !p.IsConfigured() {
		t.Fatal("IsConfigured should be true when service.json exists")
	}
}

func TestSetupStatusNamesTheMissingStep(t *testing.T) {
	t.Setenv(apiKeyEnv, "")
	p := New()
	p.serviceFile = filepath.Join(t.TempDir(), "service.json")
	if st := p.SetupStatus(); st.IsReady() || !strings.Contains(st.Detail, "opencode serve --service") || !strings.Contains(st.Detail, apiKeyEnv) {
		t.Fatalf("missing service file and key: %+v", st)
	}
	if st := newTestProvider(t, "http://127.0.0.1:1").SetupStatus(); !st.IsReady() {
		t.Fatalf("service file present: %+v", st)
	}
}

// The key alone makes the account set up, and the status never shows the key.
func TestAPIKeyAloneIsSetUp(t *testing.T) {
	t.Setenv(apiKeyEnv, "")
	p := New()
	p.serviceFile = filepath.Join(t.TempDir(), "service.json")
	if p.IsConfigured() {
		t.Fatal("no service file and no key must not be configured")
	}
	t.Setenv(apiKeyEnv, fakeKey)
	st := p.SetupStatus()
	if !st.IsReady() || strings.Contains(st.Detail, fakeKey) {
		t.Fatalf("key set: %+v", st)
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

// An account without the Go plan has nothing to show, which is not an error.
// A Go plan with no usable windows is an error.
func TestNoGoPlanIsNotAnError(t *testing.T) {
	p, _ := serve(t, 200, "application/json", `{"data":{"workspace":{"id":"w"},"budgets":[]}}`)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 0 {
		t.Fatalf("an account without the Go plan has no windows: %+v", data.Windows)
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

// A null reset is not a reset at the epoch.
func TestNullResetIsMalformed(t *testing.T) {
	p, _ := serve(t, 200, "application/json", `{"data":{"go":{"rolling":{"percent":1,"limited":false,"resetsAt":null}}}}`)
	if msg := fetchErr(t, p); !strings.Contains(msg, "malformed") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

// The usage route reads the same windows with the API key as a bearer token.
func TestAPIKeyRouteReadsTheSameWindows(t *testing.T) {
	var route usageRoute
	p := newKeyProvider(t, fakeKey, route.serve(t, 200, keyBody))
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name        string
		util        float64
		reset       time.Time
		lengthKnown bool
	}{
		{"5h", 19, time.Date(2026, 11, 19, 15, 53, 47, 0, time.UTC), true},
		// A rate-limited window reads as full, whatever its percent says.
		{"7d", 100, time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), true},
		{"monthly", 41, time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC), false},
	}
	if len(data.Windows) != len(want) {
		t.Fatalf("windows = %+v", data.Windows)
	}
	for i, w := range want {
		g := data.Windows[i]
		if g.Name != w.name || g.Utilization != w.util || !g.ResetsAt.Equal(w.reset) || g.LengthUnknown == w.lengthKnown {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
	if route.count() != 1 {
		t.Fatalf("usage route hits = %d, want 1", route.count())
	}
	if got := route.header("Authorization"); got != "Bearer "+fakeKey {
		t.Errorf("Authorization = %q", got)
	}
	if got := route.header("User-Agent"); got != "Clawmeter" {
		t.Errorf("User-Agent = %q", got)
	}
}

// roundTripFunc stands in for the HTTP transport so a test can see each request.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The other key tests point apiKeyURL at a local server. This one keeps the
// production default and records the request the route is sent.
func TestAPIKeyRouteUsesOpenCodeUsageURL(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // no service.json, so only the key route can answer
	t.Setenv(apiKeyEnv, fakeKey)
	p := New()
	var sent *http.Request
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent = r
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(keyBody)),
			Request:    r,
		}, nil
	})
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent == nil {
		t.Fatal("the key route sent no request")
	}
	if got := sent.URL.String(); got != "https://opencode.ai/zen/go/v1/usage" {
		t.Fatalf("key route requested %s", got)
	}
	if got := sent.Header.Get("Authorization"); got != "Bearer "+fakeKey {
		t.Fatalf("Authorization = %q", got)
	}
	if got := sent.Header.Get("User-Agent"); got != "Clawmeter" {
		t.Fatalf("User-Agent = %q; the usage route refuses a request without one", got)
	}
	if len(data.Windows) != 3 {
		t.Fatalf("windows = %+v, want the three in the response", data.Windows)
	}
}

// The service answers first. The key route is not called while the service works.
func TestServiceIsTriedBeforeTheAPIKey(t *testing.T) {
	const body = `{"data":{"go":{"rolling":{"percent":19,"limited":false,"resetsAt":1795115150000}}}}`
	var route usageRoute
	p, _ := serve(t, 200, "application/json", body)
	t.Setenv(apiKeyEnv, fakeKey)
	p.apiKeyURL = route.serve(t, 200, keyBody)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 19 {
		t.Fatalf("service reading not used: %+v", data.Windows)
	}
	if route.count() != 0 {
		t.Fatalf("key route called %d times while the service works", route.count())
	}
}

// An account without Go on the service has no windows; the key is not consulted.
func TestServiceWithoutGoDoesNotFallBackToTheKey(t *testing.T) {
	var route usageRoute
	p, _ := serve(t, 200, "application/json", `{"data":{"budgets":[]}}`)
	t.Setenv(apiKeyEnv, fakeKey)
	p.apiKeyURL = route.serve(t, 200, keyBody)
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.Windows) != 0 {
		t.Fatalf("data = %+v, err = %v", data, err)
	}
	if route.count() != 0 {
		t.Fatalf("key route called %d times", route.count())
	}
}

func TestAPIKeyCoversAServiceThatIsNotRunning(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	var route usageRoute
	p := newTestProvider(t, "http://"+addr)
	t.Setenv(apiKeyEnv, fakeKey)
	p.apiKeyURL = route.serve(t, 200, keyBody)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 3 || route.count() != 1 {
		t.Fatalf("windows = %d, key route hits = %d", len(data.Windows), route.count())
	}
}

func TestAPIKeyCoversAServiceWithoutConsoleSignIn(t *testing.T) {
	var route usageRoute
	p, _ := serve(t, 200, "application/json", `{"location":{"directory":"/"},"data":null}`)
	t.Setenv(apiKeyEnv, fakeKey)
	p.apiKeyURL = route.serve(t, 200, keyBody)
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.Windows) != 3 {
		t.Fatalf("data = %+v, err = %v", data, err)
	}
}

// When both routes fail, the error names both, and neither secret appears.
func TestRejectedKeyWithoutServiceNamesBothRoutes(t *testing.T) {
	var route usageRoute
	p := newKeyProvider(t, fakeKey, route.serve(t, 401, "bad key "+fakeKey))
	msg := fetchErr(t, p)
	if !strings.Contains(msg, "not found") || !strings.Contains(msg, apiKeyEnv+" was rejected") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

// OpenCode's own reader reads 403 from this route as no Go plan, not as zero.
func TestAPIKeyForbiddenMeansNoGoPlan(t *testing.T) {
	var route usageRoute
	p := newKeyProvider(t, fakeKey, route.serve(t, 403, `{"error":"no plan"}`))
	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.Windows) != 0 {
		t.Fatalf("data = %+v, err = %v", data, err)
	}
}

// The key goes only to the usage route. A redirect to another address is not followed.
func TestAPIKeyIsNotSentToARedirectTarget(t *testing.T) {
	var elsewhere usageRoute
	target := elsewhere.serve(t, 200, keyBody)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	p := newKeyProvider(t, fakeKey, redirect.URL)
	fetchErr(t, p)
	if elsewhere.count() != 0 {
		t.Fatalf("redirect target received %d requests", elsewhere.count())
	}
}

func TestAPIKeyResponseWithoutUsageIsError(t *testing.T) {
	var route usageRoute
	p := newKeyProvider(t, fakeKey, route.serve(t, 200, `{}`))
	if msg := fetchErr(t, p); !strings.Contains(msg, "no usage object") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestIdentity(t *testing.T) {
	p := New()
	if p.Name() != "opencode" || p.DisplayName() != "OpenCode" || p.Description() == "" || p.DashboardURL() != "https://opencode.ai/auth" {
		t.Fatal("unexpected identity")
	}
}
