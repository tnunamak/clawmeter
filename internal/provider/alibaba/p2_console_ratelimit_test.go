package alibaba

import (
	"context"
	"github.com/tnunamak/clawmeter/internal/provider"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestP2ConsoleRateLimitBackoff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("BAILIAN_CODING_PLAN_API_KEY", "")
	path := filepath.Join(home, "console.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"synthetic-console-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var consoleRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consoleRequests.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	newConsoleProvider := func() *Provider {
		p := newTestProvider("")
		p.consoleConfigPath = path
		p.consoleEndpoint = srv.URL
		return p
	}
	assertBackoff := func(t *testing.T, p *Provider, apiRequests *atomic.Int32) {
		t.Helper()
		for i := 0; i < 2; i++ {
			data, err := provider.FetchSource(context.Background(), p)
			if err != nil || data == nil || !strings.Contains(data.Error, "rate limited") {
				t.Fatalf("fetch %d: %#v %v", i, data, err)
			}
		}
		if consoleRequests.Load() != 1 {
			t.Fatalf("console requests %d want 1", consoleRequests.Load())
		}
		if apiRequests != nil && apiRequests.Load() != 0 {
			t.Fatalf("API requests %d want 0", apiRequests.Load())
		}
	}

	t.Run("explicit console-file source", func(t *testing.T) {
		t.Setenv("ALIBABA_CODING_PLAN_API_KEY", "")
		p := newConsoleProvider()
		p.explicitSource = true
		p.enrolledSource = true
		p.sourceID = "enrolled-console"
		p.sourceCredential.Kind = "console-file"
		p.sourceCredential.Ref = path
		assertBackoff(t, p, nil)
	})

	t.Run("auto source does not fall through to API key", func(t *testing.T) {
		consoleRequests.Store(0)
		t.Setenv("ALIBABA_CODING_PLAN_API_KEY", "synthetic-api-key")
		var apiRequests atomic.Int32
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiRequests.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer api.Close()
		p := newConsoleProvider()
		p.usageURL = api.URL
		if p.explicitSource {
			t.Fatal("test provider unexpectedly has an explicit source")
		}
		assertBackoff(t, p, &apiRequests)
	})
}
