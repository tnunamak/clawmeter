package kimik2

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

type redTeamCreditsTransport func(*http.Request) (*http.Response, error)

func (f redTeamCreditsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// No socket is opened: the response and credential are synthetic test fixtures.
func redTeamCreditsProvider(body, remainingHeader string) *Provider {
	p := New(config.ProviderConfig{APIKey: "fake-fixture-key"})
	p.httpClient = &http.Client{Transport: redTeamCreditsTransport(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		if remainingHeader != "" {
			header.Set("x-credits-remaining", remainingHeader)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header,
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	return p
}

func TestRedTeamKimiTotalWithRemainingHeader(t *testing.T) {
	p := redTeamCreditsProvider(`{"total":100}`, "40")
	got, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Windows) != 1 {
		t.Fatalf("total plus supported remaining header lost quota: %#v", got)
	}
	if got.Windows[0].Utilization != 60 {
		t.Fatalf("utilization = %v, want 60", got.Windows[0].Utilization)
	}
}

func TestRedTeamKimiTotalWithConsumed(t *testing.T) {
	p := redTeamCreditsProvider(`{"total":100,"consumed":60}`, "")
	got, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Windows) != 1 {
		t.Fatalf("known consumed and total limit lost quota: %#v", got)
	}
	if got.Windows[0].Utilization != 60 {
		t.Fatalf("utilization = %v, want 60", got.Windows[0].Utilization)
	}
}
