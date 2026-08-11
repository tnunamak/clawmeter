package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestTransformUsageUsesOnlyProviderResetMetadata(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantReset time.Time
		wantWarn  string
	}{
		{"known snake case reset", `{"quota_snapshots":{"chat":{"percentRemaining":0}},"quota_reset_date":"2026-08-01T12:34:56.123Z"}`, time.Date(2026, 8, 1, 12, 34, 56, 123000000, time.UTC), ""},
		{"known camel case reset", `{"quotaSnapshots":{"chat":{"percentRemaining":50}},"quotaResetDate":"2026-08-02"}`, time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), ""},
		{"conflicting reset keys prefer camel case", `{"quotaSnapshots":{"chat":{"percentRemaining":50}},"quotaResetDate":"2026-08-03","quota_reset_date":"2026-08-04"}`, time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), ""},
		{"missing reset keeps usage", `{"quotaSnapshots":{"premiumInteractions":{"percentRemaining":25}}}`, time.Time{}, "Copilot quota reset date is not available; reset is unknown"},
		{"malformed reset stays unknown", `{"quotaSnapshots":{"chat":{"percentRemaining":100}},"quota_reset_date":"not-a-date"}`, time.Time{}, "Copilot returned an invalid quota reset date; reset is unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response userResponse
			if err := json.Unmarshal([]byte(tt.payload), &response); err != nil {
				t.Fatal(err)
			}
			data := (&Provider{}).transformUsage(&response)
			if len(data.Windows) != 1 || !data.Windows[0].ResetsAt.Equal(tt.wantReset) {
				t.Fatalf("windows = %#v, want reset %v", data.Windows, tt.wantReset)
			}
			if data.Warning != tt.wantWarn {
				t.Fatalf("warning = %q, want %q", data.Warning, tt.wantWarn)
			}
		})
	}
}

func TestTransformUsagePreservesZeroAndIgnoresUnknownSnapshots(t *testing.T) {
	var response userResponse
	if err := json.Unmarshal([]byte(`{"quotaSnapshots":{"premiumInteractions":{"percentRemaining":100},"unknown":{"percentRemaining":-20}}}`), &response); err != nil {
		t.Fatal(err)
	}
	data := (&Provider{}).transformUsage(&response)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 {
		t.Fatalf("windows = %#v, want one zero-utilization premium window", data.Windows)
	}
	if data.Windows[0].ResetsAt.IsZero() == false {
		t.Fatal("zero-value snapshot unexpectedly received a reset")
	}
}

func TestTransformUsageDoesNotTurnMissingPercentageIntoExhaustion(t *testing.T) {
	for _, payload := range []string{
		`{"quotaSnapshots":{"chat":{}}}`,
		`{"quotaSnapshots":{"chat":{"percentRemaining":null}}}`,
	} {
		var response userResponse
		if err := json.Unmarshal([]byte(payload), &response); err != nil {
			t.Fatal(err)
		}
		data := (&Provider{}).transformUsage(&response)
		if len(data.Windows) != 0 || data.Error == "" {
			t.Fatalf("data = %#v, want unavailable usage", data)
		}
	}
}

func TestFetchUsageHTTPStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "token fixture-token" {
					t.Errorf("authorization = %q", got)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("fixture-token must not appear in errors"))
			}))
			defer server.Close()

			data, err := (&Provider{}).fetchUsage(context.Background(), server.Client(), server.URL, "fixture-token")
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				if err != nil || data == nil || !data.IsExpired {
					t.Fatalf("got data=%#v err=%v, want expired data", data, err)
				}
				return
			}
			if err == nil || data != nil || strings.Contains(err.Error(), "fixture-token") {
				t.Fatalf("got data=%#v err=%v, want redacted error", data, err)
			}
		})
	}
}

type recordingResolver struct {
	values   map[string]string
	requests []provider.SessionEnvironmentRequest
}

func (r *recordingResolver) ResolveSessionEnvironment(req provider.SessionEnvironmentRequest) map[string]string {
	r.requests = append(r.requests, req)
	out := map[string]string{}
	for _, name := range req.EnvNames {
		if value := r.values[name]; value != "" {
			out[name] = value
		}
	}
	return out
}

func TestSourceCapabilityRegistersTwoSourcesWithoutFallbackAliases(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	writeHostsJSON(t, hostsPath, "file-token")

	cfg := config.ProviderConfig{Sources: []config.SourceConfig{
		{ID: "default", Label: "Native", Credential: config.CredentialRef{Kind: "native"}},
		{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "credential-file", Ref: hostsPath}},
	}}
	registry := provider.NewRegistry()
	if err := provider.RegisterConfigured(registry, cfg, New(cfg)); err != nil {
		t.Fatal(err)
	}

	if _, ok := registry.Get("copilot"); !ok {
		t.Fatal("native default source was not registered under copilot")
	}
	work, ok := registry.Get("copilot:work")
	if !ok {
		t.Fatal("credential-file source was not registered")
	}
	if provider.SourceID(work) != "work" || provider.SourceLabel(work) != "Work" || !provider.IsEnrolledSource(work) {
		t.Fatalf("source metadata = id %q label %q enrolled %v", provider.SourceID(work), provider.SourceLabel(work), provider.IsEnrolledSource(work))
	}
	if _, ok := registry.Get("copilot:default"); ok {
		t.Fatal("default source unexpectedly had a fallback alias")
	}
}

func TestEnvNameSourceUsesResolverRequestAndNoAmbientFallback(t *testing.T) {
	t.Setenv("COPILOT_API_TOKEN", "ambient-token")
	resolver := &recordingResolver{}
	p := New(config.ProviderConfig{})
	sourceProvider, err := p.NewSource(config.ProviderConfig{}, config.SourceConfig{
		ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "COPILOT_WORK_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourced := sourceProvider.(*Provider)
	sourced.SetSessionEnvironmentResolver(resolver)

	token, err := sourced.getToken()
	if err == nil || token != "" {
		t.Fatalf("getToken = %q, %v; want missing selected env despite ambient default", token, err)
	}
	if len(resolver.requests) != 1 {
		t.Fatalf("resolver requests = %#v, want one", resolver.requests)
	}
	req := resolver.requests[0]
	if len(req.EnvNames) != 1 || req.EnvNames[0] != "COPILOT_WORK_TOKEN" || !req.AllowSessionEnvironmentFallback {
		t.Fatalf("resolver request = %#v, want exact allowlisted env recovery", req)
	}
}

func TestEnvNameSourceResolvesSelectedEnv(t *testing.T) {
	resolver := &recordingResolver{values: map[string]string{"COPILOT_WORK_TOKEN": "selected-token"}}
	sourceProvider, err := New(config.ProviderConfig{}).NewSource(config.ProviderConfig{}, config.SourceConfig{
		ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "COPILOT_WORK_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourced := sourceProvider.(*Provider)
	sourced.SetSessionEnvironmentResolver(resolver)

	token, err := sourced.getToken()
	if err != nil || token != "selected-token" {
		t.Fatalf("getToken = %q, %v; want selected resolver token", token, err)
	}
	revision := sourced.SourceRevision()
	if revision == "" || strings.Contains(revision, "COPILOT_WORK_TOKEN") || strings.Contains(revision, "selected-token") {
		t.Fatalf("revision = %q, want selector metadata without secret", revision)
	}
}

func TestCredentialFileSourceReadsExactPathAndReportsSafeRevision(t *testing.T) {
	dir := t.TempDir()
	selected := filepath.Join(dir, "hosts.json")
	other := filepath.Join(dir, "hosts.json")
	writeHostsJSON(t, selected, "selected-file-token")
	other = filepath.Join(t.TempDir(), "other.json")
	writeHostsJSON(t, other, "other-file-token")

	sourceProvider, err := New(config.ProviderConfig{}).NewSource(config.ProviderConfig{}, config.SourceConfig{
		ID: "work", Credential: config.CredentialRef{Kind: "credential-file", Ref: selected},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourced := sourceProvider.(*Provider)
	token, err := sourced.getToken()
	if err != nil || token != "selected-file-token" {
		t.Fatalf("getToken = %q, %v; want exact selected file token", token, err)
	}

	revision := sourced.SourceRevision()
	if revision == "" || strings.Contains(revision, selected) {
		t.Fatalf("revision = %q, want opaque path/stat metadata", revision)
	}
	if strings.Contains(revision, "selected-file-token") || strings.Contains(revision, "other-file-token") {
		t.Fatalf("revision = %q, want no secret data", revision)
	}
}

func TestCredentialFileSourceRequiresAbsolutePathAndGithubComEntry(t *testing.T) {
	if err := New(config.ProviderConfig{}).ValidateSource(config.SourceConfig{
		ID: "work", Credential: config.CredentialRef{Kind: "credential-file", Ref: "relative/hosts.json"},
	}); err == nil {
		t.Fatal("relative credential-file path was accepted")
	}
	if err := New(config.ProviderConfig{}).ValidateSource(config.SourceConfig{
		ID: "work", Credential: config.CredentialRef{Kind: "credential-file", Ref: filepath.Join(t.TempDir(), "other.json")},
	}); err == nil {
		t.Fatal("non-hosts.json credential-file path was accepted")
	}

	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := os.WriteFile(path, []byte(`{"example.com":{"oauth_token":"wrong-host-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceProvider, err := New(config.ProviderConfig{}).NewSource(config.ProviderConfig{}, config.SourceConfig{
		ID: "work", Credential: config.CredentialRef{Kind: "credential-file", Ref: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := sourceProvider.(*Provider).getToken()
	if err == nil || token != "" {
		t.Fatalf("getToken = %q, %v; want exact github.com hosts.json entry required", token, err)
	}
}

func writeHostsJSON(t *testing.T, path, token string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"github.com":{"oauth_token":"`+token+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}
