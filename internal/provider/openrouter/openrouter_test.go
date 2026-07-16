package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func testProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Provider{cfg: config.ProviderConfig{APIKey: "secret-token"}, client: server.Client(), creditsURL: server.URL + "/credits", keyURL: server.URL + "/key"}
}

func TestFetchUsageDocumentedEnvelopeAndKeyLimit(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("headers not documented: %v", r.Header)
		}
		if r.URL.Path == "/credits" {
			_, _ = w.Write([]byte(`{"data":{"total_credits":100.5,"total_usage":25.75,"usage":99}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":7,"usage":3,"limit_reset":"monthly"}}`))
	})
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Balances) != 1 || data.Balances[0].Remaining != 74.75 {
		t.Fatalf("balances = %#v", data.Balances)
	}
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 30 || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("windows = %#v", data.Windows)
	}
}

func TestFetchUsageOmitsUnknownAndUnlimitedLimits(t *testing.T) {
	for _, keyBody := range []string{`{"data":{"limit":null,"usage":0}}`, `{"data":{"usage":0}}`, `{"data":{"limit":0,"limit_remaining":0,"usage":0}}`} {
		t.Run(keyBody, func(t *testing.T) {
			p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/credits" {
					_, _ = w.Write([]byte(`{"data":{"total_credits":0,"total_usage":0}}`))
					return
				}
				_, _ = w.Write([]byte(keyBody))
			})
			data, err := p.FetchUsage(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(data.Windows) != 0 || data.Balances[0].Remaining != 0 {
				t.Fatalf("data = %#v", data)
			}
		})
	}
}

func TestFetchUsageErrorsAreBoundedAndRedacted(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("secret-token raw response"))
			})
			data, err := p.FetchUsage(context.Background())
			if status == 401 || status == 403 {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("data=%#v err=%v", data, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":{"total_credits":1}}`)) })
	_, err := p.FetchUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing credits fields") {
		t.Fatalf("err = %v", err)
	}
}
