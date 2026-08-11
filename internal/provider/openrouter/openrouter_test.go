package openrouter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func providerServer(t *testing.T, handler http.HandlerFunc, management bool) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := &Provider{cfg: config.ProviderConfig{APIKey: "standard-secret"}, client: server.Client(), creditsURL: server.URL + "/credits", keyURL: server.URL + "/key"}
	if management {
		p.managementKey = "management-secret"
	}
	return p
}

func TestStandardKeyUsesKeyOnlyAndComputesCapFromRemaining(t *testing.T) {
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/credits" {
			t.Errorf("standard key must not call credits")
		}
		if r.Header.Get("Authorization") != "Bearer standard-secret" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":7,"usage":99,"limit_reset":"monthly"}}`))
	}, false)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Balances) != 0 || len(data.Windows) != 1 || data.Windows[0].Utilization != 30 || data.Windows[0].ResetPolicy != "monthly" || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("data = %#v", data)
	}
}

func TestManagementKeyOnlyUsesCredits(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key" {
			t.Errorf("management-only must not call key")
			return
		}
		if r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":{"total_credits":100.5,"total_usage":25.75}}`))
	}, true)
	p.cfg.APIKey = ""
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Balances) != 1 || data.Balances[0].Remaining != 74.75 || len(data.Windows) != 0 {
		t.Fatalf("data = %#v", data)
	}
}

func TestBothKeysFetchIndependentSurfaces(t *testing.T) {
	seen := map[string]string{}
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Get("Authorization")
		if r.URL.Path == "/key" {
			_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":0,"usage":0}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"total_credits":1,"total_usage":0}}`))
	}, true)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seen["/key"] != "Bearer standard-secret" || seen["/credits"] != "Bearer management-secret" || len(data.Balances) != 1 || data.Windows[0].Utilization != 100 {
		t.Fatalf("seen=%v data=%#v", seen, data)
	}
}

func TestCredentialSurfaceStatusesAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		management bool
		key        string
		path       string
		status     int
	}{
		{"standard-401", false, "standard-secret", "/key", 401}, {"standard-403", false, "standard-secret", "/key", 403},
		{"standard-429", false, "standard-secret", "/key", 429}, {"management-401", true, "", "/credits", 401}, {"management-403", true, "", "/credits", 403},
		{"management-429", true, "", "/credits", 429}, {"management-500", true, "", "/credits", 500}, {"management-503", true, "", "/credits", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENROUTER_API_KEY", "")
			p := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte("standard-secret management-secret"))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"limit":10,"limit_remaining":5,"usage":0}}`))
			}, tc.management)
			p.cfg.APIKey = tc.key
			data, err := p.FetchUsage(context.Background())
			if tc.status == 401 || tc.status == 403 {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("data=%#v err=%v", data, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestMalformedCreditsAndMissingFields(t *testing.T) {
	p := providerServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":{"total_credits":1}}`)) }, true)
	p.cfg.APIKey = ""
	_, err := p.FetchUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing credits fields") {
		t.Fatalf("err=%v", err)
	}
}

func TestSourceCapabilityListsAndValidatesOpenRouterKinds(t *testing.T) {
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("expected source capability")
	}
	kinds := capability.SourceKinds()
	if len(kinds) != 3 || kinds[0].Kind != "native" || kinds[1].Kind != "api-key-env-name" || kinds[2].Kind != "management-key-env-name" {
		t.Fatalf("kinds = %#v", kinds)
	}
	defaultSource, ok := capability.DefaultSource()
	if !ok || defaultSource.ID != "default" || defaultSource.Label != "Default" || defaultSource.Credential.Kind != "native" {
		t.Fatalf("default source = %#v, ok=%v", defaultSource, ok)
	}
	for _, tc := range []struct {
		name   string
		source config.SourceConfig
		valid  bool
	}{
		{"native default", config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}, true},
		{"native named", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}, false},
		{"api env", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "api-key-env-name", Ref: "OPENROUTER_WORK_API_KEY"}}, true},
		{"management env", config.SourceConfig{ID: "wallet", Credential: config.CredentialRef{Kind: "management-key-env-name", Ref: "OPENROUTER_WALLET_KEY"}}, true},
		{"empty env", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "api-key-env-name"}}, false},
		{"unknown", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "OPENROUTER_API_KEY"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := capability.ValidateSource(tc.source)
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateSource() error = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestExplicitSourceIdentityEnrollmentAndRevision(t *testing.T) {
	capability, _ := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	p, err := capability.NewSource(config.ProviderConfig{}, config.SourceConfig{
		ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "api-key-env-name", Ref: "OPENROUTER_WORK_API_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.SourceID(p) != "work" || provider.SourceLabel(p) != "Work" || !provider.IsEnrolledSource(p) {
		t.Fatalf("source identity = %q/%q enrolled=%v", provider.SourceID(p), provider.SourceLabel(p), provider.IsEnrolledSource(p))
	}
	revision := provider.SourceRevision(p)
	if revision == "" || strings.Contains(revision, "OPENROUTER_WORK_API_KEY") {
		t.Fatalf("revision = %q", revision)
	}
	if provider.SourceRevision(p) != revision {
		t.Fatalf("revision is not stable")
	}
	native, err := capability.NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}})
	if err != nil {
		t.Fatal(err)
	}
	if provider.SourceRevision(native) != "" {
		t.Fatalf("native revision = %q", provider.SourceRevision(native))
	}
}

type openrouterSessionEnvironmentResolver struct {
	values   map[string]string
	requests []provider.SessionEnvironmentRequest
}

func (r *openrouterSessionEnvironmentResolver) ResolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	r.requests = append(r.requests, request)
	values := map[string]string{}
	for _, name := range request.EnvNames {
		if value := r.values[name]; value != "" {
			values[name] = value
		}
	}
	return values
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestExplicitAPIKeyEnvSourceResolverRequestAndNoAmbientFallback(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "ambient-standard")
	t.Setenv("OPENROUTER_MANAGEMENT_KEY", "ambient-management")
	resolver := &openrouterSessionEnvironmentResolver{values: map[string]string{
		"OPENROUTER_WORK_API_KEY": "selected-standard",
	}}
	p := New(config.ProviderConfig{APIKey: "configured-standard"})
	p.managementKey = "configured-management"
	p.sourceID = "work"
	p.sourceLabel = "Work"
	p.sourceCredential = config.CredentialRef{Kind: "api-key-env-name", Ref: "OPENROUTER_WORK_API_KEY"}
	p.explicitSource = true
	p.enrolledSource = true
	p.SetSessionEnvironmentResolver(resolver)
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != keyURL {
			t.Fatalf("unexpected URL %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer selected-standard" {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		return response(200, `{"data":{"limit":10,"limit_remaining":6,"usage":4}}`), nil
	})}
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.SourceID != "work" || data.SourceLabel != "Work" || len(data.Windows) != 1 || len(data.Balances) != 0 {
		t.Fatalf("data = %#v", data)
	}
	if len(resolver.requests) != 1 || strings.Join(resolver.requests[0].EnvNames, ",") != "OPENROUTER_WORK_API_KEY" || !resolver.requests[0].AllowSessionEnvironmentFallback {
		t.Fatalf("requests = %#v", resolver.requests)
	}
}

func TestExplicitTwoSourcesDoNotFallbackAcrossOpenRouterKeySurfaces(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "ambient-standard")
	t.Setenv("OPENROUTER_MANAGEMENT_KEY", "ambient-management")
	resolver := &openrouterSessionEnvironmentResolver{values: map[string]string{
		"OPENROUTER_WORK_API_KEY":        "selected-standard",
		"OPENROUTER_WALLET_MANAGEMENT":   "selected-management",
		"OPENROUTER_UNUSED_MANAGEMENT":   "wrong-management",
		"OPENROUTER_UNUSED_STANDARD_KEY": "wrong-standard",
	}}
	seen := map[string]string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.String()] = r.Header.Get("Authorization")
		switch r.URL.String() {
		case keyURL:
			return response(200, `{"data":{"limit":10,"limit_remaining":5,"usage":5}}`), nil
		case creditsURL:
			return response(200, `{"data":{"total_credits":8,"total_usage":3}}`), nil
		default:
			t.Fatalf("unexpected URL %s", r.URL.String())
			return nil, nil
		}
	})}
	apiSource := New(config.ProviderConfig{APIKey: "configured-standard"})
	apiSource.managementKey = "configured-management"
	apiSource.client = client
	apiSource.sourceID = "work"
	apiSource.sourceLabel = "Work"
	apiSource.sourceCredential = config.CredentialRef{Kind: "api-key-env-name", Ref: "OPENROUTER_WORK_API_KEY"}
	apiSource.explicitSource = true
	apiSource.enrolledSource = true
	apiSource.SetSessionEnvironmentResolver(resolver)
	managementSource := New(config.ProviderConfig{APIKey: "configured-standard"})
	managementSource.managementKey = "configured-management"
	managementSource.client = client
	managementSource.sourceID = "wallet"
	managementSource.sourceLabel = "Wallet"
	managementSource.sourceCredential = config.CredentialRef{Kind: "management-key-env-name", Ref: "OPENROUTER_WALLET_MANAGEMENT"}
	managementSource.explicitSource = true
	managementSource.enrolledSource = true
	managementSource.SetSessionEnvironmentResolver(resolver)

	apiData, err := apiSource.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	managementData, err := managementSource.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seen[keyURL] != "Bearer selected-standard" || seen[creditsURL] != "Bearer selected-management" {
		t.Fatalf("seen = %#v", seen)
	}
	if len(apiData.Windows) != 1 || len(apiData.Balances) != 0 || len(managementData.Balances) != 1 || len(managementData.Windows) != 0 {
		t.Fatalf("api=%#v management=%#v", apiData, managementData)
	}
	if len(resolver.requests) != 2 ||
		strings.Join(resolver.requests[0].EnvNames, ",") != "OPENROUTER_WORK_API_KEY" ||
		strings.Join(resolver.requests[1].EnvNames, ",") != "OPENROUTER_WALLET_MANAGEMENT" ||
		!resolver.requests[0].AllowSessionEnvironmentFallback ||
		!resolver.requests[1].AllowSessionEnvironmentFallback {
		t.Fatalf("requests = %#v", resolver.requests)
	}
}
