package status

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type statusTestTransport func(*http.Request) (*http.Response, error)

func (f statusTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWatchedComponentStatus(t *testing.T) {
	for _, tc := range []struct {
		name, components string
		want             Indicator
	}{
		{"empty", "[]", Unknown},
		{"unmatched", `[{"name":"Other","status":"operational"}]`, Unknown},
		{"unknown", `[{"name":"Watched","status":"new_status"}]`, Unknown},
		{"unknown_with_operational", `[{"name":"Watched","status":"operational"},{"name":"Also","status":"new_status"}]`, Unknown},
		{"unknown_with_outage", `[{"name":"Watched","status":"major_outage"},{"name":"Also","status":"new_status"}]`, Unknown},
		{"operational", `[{"name":"Watched","status":"operational"}]`, None},
		{"outage", `[{"name":"Watched","status":"major_outage"}]`, Major},
		{"unwatched_unknown", `[{"name":"Watched","status":"operational"},{"name":"Other","status":"new_status"}]`, None},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			http.DefaultTransport = statusTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/api/v2/components.json" {
					t.Errorf("unexpected request: %s", req.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"components":` + tc.components + `}`)), Header: make(http.Header)}, nil
			})
			got := fetchComponents(context.Background(), statusPageConfig{BaseURL: "https://status.invalid", Components: []string{"Watched", "Also"}})
			if got.Indicator != tc.want {
				t.Errorf("got %s, want %s", got.Indicator, tc.want)
			}
		})
	}
}
