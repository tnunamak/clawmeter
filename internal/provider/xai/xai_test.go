package xai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestTransformBalance_ComputesCreditUsage(t *testing.T) {
	p := New(config.ProviderConfig{})
	data := p.transformBalance(&prepaidBalanceResponse{
		Changes: []balanceChange{
			{TopupStatus: "SUCCEEDED", Amount: centsValue{Val: -2500}},
			{TopupStatus: "SUCCEEDED", Amount: centsValue{Val: 500}},
			{TopupStatus: "FAILED", Amount: centsValue{Val: -1000}},
		},
		Total: centsValue{Val: -1000},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(data.Windows))
	}
	w := data.Windows[0]
	if w.Name != "credits" {
		t.Fatalf("window name = %q, want credits", w.Name)
	}
	if w.Limit != 2500 {
		t.Fatalf("limit = %d, want 2500", w.Limit)
	}
	if w.Used != 1500 {
		t.Fatalf("used = %d, want 1500", w.Used)
	}
	if w.Utilization != 60 {
		t.Fatalf("utilization = %.2f, want 60", w.Utilization)
	}
}

func TestCentsValue_UnmarshalStringAndNumber(t *testing.T) {
	for _, input := range []string{`{"val":"-1000"}`, `{"val":-1000}`} {
		var c centsValue
		if err := json.Unmarshal([]byte(input), &c); err != nil {
			t.Fatalf("Unmarshal(%s): %v", input, err)
		}
		if c.Val != -1000 {
			t.Fatalf("Unmarshal(%s) = %d, want -1000", input, c.Val)
		}
	}
}

func TestFetchUsage_UsesConfiguredTeamID(t *testing.T) {
	var sawValidation bool
	var sawBalance bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer mgmt-key" {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/auth/management-keys/validation":
			sawValidation = true
			t.Fatal("validation endpoint should not be called when team id is configured")
		case "/v1/billing/teams/team-123/prepaid/balance":
			sawBalance = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"changes":[{"topupStatus":"SUCCEEDED","amount":{"val":"-1000"}}],"total":{"val":"-250"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{
		APIKey: "mgmt-key",
		Extra:  map[string]interface{}{"team_id": "team-123"},
	})
	p.baseURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if !sawBalance || sawValidation {
		t.Fatalf("sawBalance=%v sawValidation=%v", sawBalance, sawValidation)
	}
	if got := data.Windows[0].Utilization; got != 75 {
		t.Fatalf("utilization = %.2f, want 75", got)
	}
}

func TestFetchUsage_DiscoversTeamIDFromManagementKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/management-keys/validation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"SCOPE_TEAM","scopeId":"team-abc"}`))
		case "/v1/billing/teams/team-abc/prepaid/balance":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"changes":[{"topupStatus":"SUCCEEDED","amount":{"val":"-1000"}}],"total":{"val":"-1000"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{APIKey: "mgmt-key"})
	p.baseURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if got := data.Windows[0].Utilization; got != 0 {
		t.Fatalf("utilization = %.2f, want 0", got)
	}
}

func TestFetchUsage_UnauthorizedReturnsExpiredData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{APIKey: "bad-key"})
	p.baseURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if !data.IsExpired {
		t.Fatal("unauthorized response should mark data expired")
	}
	if data.Error == "" {
		t.Fatal("unauthorized response should include an error")
	}
}

func TestSetupStatus_EnvManagementKey(t *testing.T) {
	t.Setenv("XAI_MANAGEMENT_API_KEY", "key")
	p := New(config.ProviderConfig{})
	if !p.IsConfigured() {
		t.Fatal("provider should be configured from XAI_MANAGEMENT_API_KEY")
	}
	if !p.SetupStatus().IsReady() {
		t.Fatalf("SetupStatus = %#v, want ready", p.SetupStatus())
	}
}

func TestConfiguredTeamID_FromEnv(t *testing.T) {
	t.Setenv("XAI_TEAM_ID", "team-env")
	p := New(config.ProviderConfig{})
	got, err := p.configuredTeamID()
	if err != nil {
		t.Fatalf("configuredTeamID: %v", err)
	}
	if got != "team-env" {
		t.Fatalf("team id = %q, want team-env", got)
	}
}

func TestManagementKey_DoesNotUseInferenceAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "inference-key")
	p := New(config.ProviderConfig{})
	if _, err := p.managementKey(); err == nil {
		t.Fatal("XAI_API_KEY must not be accepted as a management key")
	}
}
