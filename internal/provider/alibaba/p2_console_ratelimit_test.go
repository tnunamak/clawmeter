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
	t.Setenv("ALIBABA_CODING_PLAN_API_KEY", "")
	t.Setenv("BAILIAN_CODING_PLAN_API_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LOCALAPPDATA", home)
	path := filepath.Join(home, "console.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"synthetic-console-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	p := newTestProvider("")
	p.consoleConfigPath = path
	p.consoleEndpoint = srv.URL
	for i := 0; i < 2; i++ {
		data, err := provider.FetchSource(context.Background(), p)
		if err != nil || data == nil || !strings.Contains(data.Error, "rate limited") {
			t.Fatalf("fetch %d: %#v %v", i, data, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("upstream requests %d want 1", requests.Load())
	}
}
