package kimik2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

type kimik2SessionEnvironmentResolver struct {
	request provider.SessionEnvironmentRequest
	values  map[string]string
}

func (r *kimik2SessionEnvironmentResolver) ResolveSessionEnvironment(request provider.SessionEnvironmentRequest) map[string]string {
	r.request = request
	return r.values
}

func TestSourceCapabilityListsAndValidatesKimiK2Kinds(t *testing.T) {
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("Kimi K2 provider did not expose source capability")
	}
	kinds := capability.SourceKinds()
	if len(kinds) != 2 || kinds[0].Kind != "native" || kinds[1].Kind != "env-name" {
		t.Fatalf("source kinds = %#v", kinds)
	}
	for _, tc := range []struct {
		name   string
		source config.SourceConfig
		valid  bool
	}{
		{"native default", config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}, true},
		{"native named", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}, false},
		{"env name", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "KIMI_K2_WORK_KEY"}}, true},
		{"bad env name", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "KIMI-K2-WORK-KEY"}}, false},
		{"credential file unsupported", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "credential-file", Ref: "/abs/kimi-code.json"}}, false},
	} {
		err := capability.ValidateSource(tc.source)
		if (err == nil) != tc.valid {
			t.Errorf("%s: error = %v, valid = %v", tc.name, err, tc.valid)
		}
	}
}

func TestExplicitEnvNameUsesOnlyResolverSelection(t *testing.T) {
	t.Setenv("KIMI_K2_SELECTED_KEY", "ambient")
	resolver := &kimik2SessionEnvironmentResolver{values: map[string]string{"KIMI_K2_SELECTED_KEY": "resolved"}}
	p := NewSource(config.ProviderConfig{APIKey: "configured"}, config.SourceConfig{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "env-name", Ref: "KIMI_K2_SELECTED_KEY"}})
	p.SetSessionEnvironmentResolver(resolver)

	key, err := p.getAPIKey()
	if err != nil || key != "resolved" {
		t.Fatalf("explicit env key = %q, %v", key, err)
	}
	if len(resolver.request.EnvNames) != 1 || resolver.request.EnvNames[0] != "KIMI_K2_SELECTED_KEY" || !resolver.request.AllowSessionEnvironmentFallback {
		t.Fatalf("resolver request = %#v", resolver.request)
	}
	if p.SourceID() != "work" || p.SourceLabel() != "Work" || !p.IsEnrolledSource() {
		t.Fatalf("source identity = %q/%q enrolled=%t", p.SourceID(), p.SourceLabel(), p.IsEnrolledSource())
	}
	if rev := p.SourceRevision(); rev == "" || strings.Contains(rev, "KIMI_K2_SELECTED_KEY") || strings.Contains(rev, "resolved") {
		t.Fatalf("source revision = %q, want opaque env nonce-bound value", rev)
	}
}

func TestFetchUsageUsesInjectedTransportAndBearerToken(t *testing.T) {
	resolver := &kimik2SessionEnvironmentResolver{values: map[string]string{"KIMI_K2_SELECTED_KEY": "selected-secret"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer selected-secret" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"consumed":0,"remaining":10}`))
	}))
	defer server.Close()

	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "KIMI_K2_SELECTED_KEY"}})
	p.SetSessionEnvironmentResolver(resolver)
	p.creditsURL = server.URL
	p.httpClient = server.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil || len(data.Windows) != 1 || data.Windows[0].Utilization != 0 {
		t.Fatalf("FetchUsage() = %#v, %v", data, err)
	}
}

func TestExtractCreditsTracksNumericPresence(t *testing.T) {
	p := &Provider{}
	consumed, hasConsumed, remaining, hasRemaining := p.extractCredits(map[string]interface{}{
		"consumed":  float64(0),
		"remaining": float64(10),
	})
	if !hasConsumed || !hasRemaining || consumed != 0 || remaining != 10 {
		t.Fatalf("extractCredits() = %v/%t, %v/%t", consumed, hasConsumed, remaining, hasRemaining)
	}

	_, hasConsumed, _, hasRemaining = p.extractCredits(map[string]interface{}{"unrelated": float64(0)})
	if hasConsumed || hasRemaining {
		t.Fatal("absent credit values were reported as present")
	}
}
