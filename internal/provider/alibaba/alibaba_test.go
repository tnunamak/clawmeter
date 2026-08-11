package alibaba

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/shellpath"
)

var testNow = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func newTestProvider(url string) *Provider {
	p := New(config.ProviderConfig{})
	p.usageURL = url
	p.now = func() time.Time { return testNow }
	return p
}

func quotaResponse() map[string]any {
	return map[string]any{
		"code": "200",
		"data": map[string]any{
			"codingPlanInstanceInfos": []any{
				map[string]any{
					"status":   "VALID",
					"planName": "Pro",
					"codingPlanQuotaInfo": map[string]any{
						"per5HourUsedQuota":                float64(50),
						"per5HourTotalQuota":               float64(200),
						"per5HourQuotaNextRefreshTime":     float64(testNow.Add(3 * time.Hour).UnixMilli()),
						"perWeekUsedQuota":                 float64(500),
						"perWeekTotalQuota":                float64(2000),
						"perWeekQuotaNextRefreshTime":      float64(testNow.Add(5 * 24 * time.Hour).UnixMilli()),
						"perBillMonthUsedQuota":            float64(1000),
						"perBillMonthTotalQuota":           float64(8000),
						"perBillMonthQuotaNextRefreshTime": float64(testNow.Add(20 * 24 * time.Hour).UnixMilli()),
					},
				},
			},
		},
	}
}

func TestFetchUsage_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got == "" {
			t.Error("missing Authorization header")
		}
		if got := r.Header.Get("x-api-key"); got == "" {
			t.Error("missing x-api-key header")
		}
		if got := r.Header.Get("X-DashScope-API-Key"); got == "" {
			t.Error("missing X-DashScope-API-Key header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(quotaResponse())
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if data.IsExpired {
		t.Fatal("unexpected IsExpired")
	}
	if len(data.Windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(data.Windows))
	}

	tests := []struct {
		name        string
		displayName string
		used        int
		limit       int
		utilization float64
	}{
		{"session_5h", "5-Hour", 50, 200, 25},
		{"weekly", "Weekly", 500, 2000, 25},
		{"monthly", "Monthly", 1000, 8000, 12.5},
	}
	for i, tt := range tests {
		w := data.Windows[i]
		if w.Name != tt.name {
			t.Errorf("window %d name = %q, want %q", i, w.Name, tt.name)
		}
		if w.DisplayName != tt.displayName {
			t.Errorf("window %d displayName = %q, want %q", i, w.DisplayName, tt.displayName)
		}
		if w.Used != tt.used {
			t.Errorf("window %d used = %d, want %d", i, w.Used, tt.used)
		}
		if w.Limit != tt.limit {
			t.Errorf("window %d limit = %d, want %d", i, w.Limit, tt.limit)
		}
		if w.Utilization != tt.utilization {
			t.Errorf("window %d utilization = %f, want %f", i, w.Utilization, tt.utilization)
		}
		if w.ResetsAt.IsZero() {
			t.Errorf("window %d resetsAt should not be zero", i)
		}
	}
}

func TestFetchUsage_ConsoleSessionUsesOnlyReadOnlyQuotaOperation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"active_config":"coding-plan","coding-plan":{"access_token":"console-token","console_region":"ap-southeast-1","console_site":"international"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer console-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		params := r.Form.Get("params")
		if !strings.Contains(params, codingPlanConsoleOperation) {
			t.Fatalf("unexpected console operation: %s", params)
		}
		if strings.Contains(params, "/consume") || strings.Contains(params, "/use") || strings.Contains(params, "purchase") {
			t.Fatalf("unsafe console operation: %s", params)
		}
		if !strings.Contains(params, intlCommodityCode) || !strings.Contains(params, `"onlyLatestOne":true`) {
			t.Fatalf("console request missing Coding Plan query: %s", params)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(quotaResponse())
	}))
	defer server.Close()

	p := newTestProvider("")
	p.consoleConfigPath = configPath
	p.consoleEndpoint = server.URL
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 3 || data.Windows[0].Utilization != 25 {
		t.Fatalf("console usage = %#v", data)
	}
}

func TestFetchUsage_ConsoleSessionWithoutPlanStaysQuiet(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"access_token":"console-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"DataV2":{"data":{"data":{"codingPlanInstanceInfos":[]}}}}}`))
	}))
	defer server.Close()

	p := newTestProvider("")
	p.consoleConfigPath = configPath
	p.consoleEndpoint = server.URL
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.Error != "" || len(data.Windows) != 0 || data.IsExpired {
		t.Fatalf("no-plan console data = %#v", data)
	}
}

func TestFetchUsage_ExpiredConsoleSessionInvalidatesUsage(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"access_token":"console-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"success":false,"errorCode":"BailianGateway.Login.NotLogined"}}`))
	}))
	defer server.Close()

	p := newTestProvider("")
	p.consoleConfigPath = configPath
	p.consoleEndpoint = server.URL
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !data.IsExpired || !data.InvalidatesPriorUsage || !strings.Contains(data.Error, "expired") {
		t.Fatalf("expired console data = %#v", data)
	}
}

func TestFetchUsage_ConsoleRejectionIsNotTreatedAsMissingPlan(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"access_token":"console-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"success":false,"errorCode":"BailianGateway.Permission.Denied"}}`))
	}))
	defer server.Close()

	p := newTestProvider("")
	p.consoleConfigPath = configPath
	p.consoleEndpoint = server.URL
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.Error == "" || data.IsExpired {
		t.Fatalf("rejected console data = %#v", data)
	}
}

func TestFetchUsage_AltFieldNames(t *testing.T) {
	resp := map[string]any{
		"codingPlanInstanceInfos": []any{
			map[string]any{
				"status": "VALID",
				"codingPlanQuotaInfo": map[string]any{
					"perFiveHourUsedQuota":         float64(10),
					"perFiveHourTotalQuota":        float64(100),
					"perWeekUsedQuota":             float64(20),
					"perWeekTotalQuota":            float64(500),
					"perMonthUsedQuota":            float64(30),
					"perMonthTotalQuota":           float64(3000),
					"perMonthQuotaNextRefreshTime": "2026-08-15T00:00:00Z",
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if len(data.Windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(data.Windows))
	}
	if data.Windows[0].Used != 10 || data.Windows[0].Limit != 100 {
		t.Errorf("5h window: used=%d limit=%d", data.Windows[0].Used, data.Windows[0].Limit)
	}
	// Monthly reset parsed from ISO string
	if data.Windows[2].ResetsAt.IsZero() {
		t.Error("monthly resetsAt should be parsed from ISO string")
	}
}

func TestFetchUsage_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-bad-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for auth failure, got %v", err)
	}
	if !data.IsExpired {
		t.Fatal("expected IsExpired=true for 401")
	}
	if !data.InvalidatesPriorUsage {
		t.Error("expected InvalidatesPriorUsage=true for auth failure")
	}
	if data.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestFetchUsage_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-bad-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for 403, got %v", err)
	}
	if !data.IsExpired {
		t.Fatal("expected IsExpired=true for 403")
	}
	if !data.InvalidatesPriorUsage {
		t.Error("expected InvalidatesPriorUsage=true for auth failure")
	}
}

func TestFetchUsage_LoginRequiredMarksCredentialsExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"LoginRequired","Message":"login required"}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for login-required response, got %v", err)
	}
	if !data.IsExpired || !data.InvalidatesPriorUsage {
		t.Fatalf("login-required data = %+v, want expired and invalidating", data)
	}
	if data.Error == "" {
		t.Fatal("expected an actionable error")
	}
}

func TestFetchUsage_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	_, err := p.FetchUsage(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestFetchUsage_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	_, err := p.FetchUsage(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestFetchUsage_MissingQuotaData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": "200", "data": map[string]any{}})
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	_, err := p.FetchUsage(context.Background())
	if err == nil {
		t.Fatal("expected error for missing quota data")
	}
}

func TestFetchUsage_DoubleEncodedJSON(t *testing.T) {
	inner, _ := json.Marshal(quotaResponse())
	resp := map[string]any{
		"code": "200",
		"data": string(inner),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage with double-encoded JSON: %v", err)
	}
	if len(data.Windows) != 3 {
		t.Fatalf("expected 3 windows from double-encoded response, got %d", len(data.Windows))
	}
}

func TestFetchUsage_InstanceSelection(t *testing.T) {
	resp := map[string]any{
		"codingPlanInstanceInfos": []any{
			map[string]any{
				"status": "EXPIRED",
				"codingPlanQuotaInfo": map[string]any{
					"per5HourUsedQuota":  float64(999),
					"per5HourTotalQuota": float64(999),
				},
			},
			map[string]any{
				"status":   "VALID",
				"isActive": true,
				"codingPlanQuotaInfo": map[string]any{
					"per5HourUsedQuota":  float64(10),
					"per5HourTotalQuota": float64(100),
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if len(data.Windows) == 0 {
		t.Fatal("expected at least one window")
	}
	if data.Windows[0].Used != 10 {
		t.Errorf("expected VALID instance (used=10), got used=%d", data.Windows[0].Used)
	}
}

func TestFetchUsage_LoginRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": "needlogin"})
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("expected structured auth failure, got %v", err)
	}
	if !data.IsExpired || !data.InvalidatesPriorUsage {
		t.Fatalf("login-required data = %+v, want expired and invalidating", data)
	}
}

func TestFetchUsage_FiveHourResetNormalization(t *testing.T) {
	// Reset time <60s from now should be pushed forward by 5 hours.
	soonReset := float64(testNow.Add(30 * time.Second).UnixMilli())
	resp := map[string]any{
		"codingPlanInstanceInfos": []any{
			map[string]any{
				"status": "VALID",
				"codingPlanQuotaInfo": map[string]any{
					"per5HourUsedQuota":            float64(10),
					"per5HourTotalQuota":           float64(100),
					"per5HourQuotaNextRefreshTime": soonReset,
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if len(data.Windows) == 0 {
		t.Fatal("expected at least one window")
	}
	reset := data.Windows[0].ResetsAt
	if reset.IsZero() {
		t.Fatal("resetsAt should not be zero")
	}
	if reset.Sub(testNow) < 4*time.Hour {
		t.Errorf("5h reset should be normalized forward, got %v from now", reset.Sub(testNow))
	}
}

func TestFetchUsage_PartialWindows(t *testing.T) {
	// Only weekly has complete data; 5h missing total, monthly missing used.
	resp := map[string]any{
		"codingPlanInstanceInfos": []any{
			map[string]any{
				"status": "VALID",
				"codingPlanQuotaInfo": map[string]any{
					"per5HourUsedQuota":      float64(10),
					"perWeekUsedQuota":       float64(100),
					"perWeekTotalQuota":      float64(1000),
					"perBillMonthTotalQuota": float64(5000),
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if len(data.Windows) != 1 {
		t.Fatalf("expected 1 complete window, got %d", len(data.Windows))
	}
	if data.Windows[0].Name != "weekly" {
		t.Errorf("expected weekly window, got %q", data.Windows[0].Name)
	}
}

func TestFetchUsage_ZeroTotalSkipped(t *testing.T) {
	resp := map[string]any{
		"codingPlanInstanceInfos": []any{
			map[string]any{
				"status": "VALID",
				"codingPlanQuotaInfo": map[string]any{
					"per5HourUsedQuota":  float64(0),
					"per5HourTotalQuota": float64(0),
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if len(data.Windows) != 0 {
		t.Fatalf("expected 0 windows for zero total, got %d", len(data.Windows))
	}
	if data.Error == "" {
		t.Error("expected error message for no complete data")
	}
}

func TestIsConfigured(t *testing.T) {
	tests := []struct {
		name   string
		cfgKey string
		envVar string
		envVal string
		want   bool
	}{
		{"config key", "sk-sp-test", "", "", true},
		{"env ALIBABA_CODING_PLAN_API_KEY", "", "ALIBABA_CODING_PLAN_API_KEY", "sk-sp-env", true},
		{"env BAILIAN_CODING_PLAN_API_KEY", "", "BAILIAN_CODING_PLAN_API_KEY", "sk-sp-env", true},
		{"generic DashScope key ignored", "", "DASHSCOPE_API_KEY", "sk-sp-env", false},
		{"Token Plan key ignored", "", "BAILIAN_TOKEN_PLAN_API_KEY", "sk-sp-token-plan", false},
		{"wrong Coding Plan key shape", "", "BAILIAN_CODING_PLAN_API_KEY", "sk-ws-env", false},
		{"nothing", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			if tt.envVar != "" {
				t.Setenv(tt.envVar, tt.envVal)
			}
			// Clear other Coding Plan variables that might interfere.
			for _, name := range envVarNames {
				if name != tt.envVar {
					t.Setenv(name, "")
				}
			}
			p := New(config.ProviderConfig{APIKey: tt.cfgKey})
			if got := p.IsConfigured(); got != tt.want {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnrolledSourcesNeverBorrowAmbientAlibabaCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ambientDir := filepath.Join(home, ".bailian")
	if err := os.MkdirAll(ambientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ambientDir, "config.json"), []byte(`{"access_token":"ambient-console"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALIBABA_CODING_PLAN_API_KEY", "sk-sp-ambient")

	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("Alibaba did not expose source capability")
	}
	console, err := capability.NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Label: "Work", Credential: config.CredentialRef{Kind: "console-file", Ref: filepath.Join(home, "missing.json")}})
	if err != nil {
		t.Fatal(err)
	}
	if console.IsConfigured() {
		t.Fatal("explicit console source borrowed ambient console or API-key credentials")
	}
	data, err := console.FetchUsage(context.Background())
	if err != nil || data.SourceID != "work" || data.SourceLabel != "Work" || !data.IsExpired {
		t.Fatalf("missing explicit console result = %#v, %v", data, err)
	}

	keySource, err := capability.NewSource(config.ProviderConfig{APIKey: "sk-sp-config"}, config.SourceConfig{ID: "team", Credential: config.CredentialRef{Kind: "api-key-env-name", Ref: "ALIBABA_TEAM_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	if keySource.IsConfigured() {
		t.Fatal("explicit API-key source borrowed config, ambient env, or console credentials")
	}
	t.Setenv("ALIBABA_TEAM_KEY", "sk-sp-team")
	if !keySource.IsConfigured() {
		t.Fatal("selected API-key environment variable was not used")
	}
}

func TestIsConfigured_QwenSettings(t *testing.T) {
	for _, name := range envVarNames {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	qwenDir := filepath.Join(home, ".qwen")
	os.MkdirAll(qwenDir, 0o755)
	settings := map[string]any{
		"env": map[string]string{
			"BAILIAN_CODING_PLAN_API_KEY": "sk-sp-from-settings",
		},
	}
	raw, _ := json.Marshal(settings)
	os.WriteFile(filepath.Join(qwenDir, "settings.json"), raw, 0o644)

	p := New(config.ProviderConfig{})
	if !p.IsConfigured() {
		t.Error("expected IsConfigured()=true from ~/.qwen/settings.json")
	}
}

func TestIsConfigured_RecoversDeclaredEnvFromLoginShell(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	for _, name := range envVarNames {
		t.Setenv(name, "")
	}
	zdotdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdotdir, ".zshrc"), []byte("export BAILIAN_CODING_PLAN_API_KEY='sk-sp-provider-test-value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", zsh)
	t.Setenv("ZDOTDIR", zdotdir)

	p := New(config.ProviderConfig{})
	p.SetSessionEnvironmentResolver(shellpath.NewSessionEnvironmentResolver())
	if !p.IsConfigured() {
		t.Fatal("Alibaba provider did not become configured from its declared shell environment")
	}
	if os.Getenv("BAILIAN_CODING_PLAN_API_KEY") != "" {
		t.Fatal("recovered Alibaba credential leaked into the process environment")
	}
}

func TestSetupStatus(t *testing.T) {
	for _, name := range envVarNames {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	p := New(config.ProviderConfig{})
	status := p.SetupStatus()
	if status.State != provider.SetupNeedsAuth {
		t.Errorf("expected needs_auth, got %s", status.State)
	}

	p.cfg.APIKey = "sk-sp-test"
	status = p.SetupStatus()
	if status.State != provider.SetupReady {
		t.Errorf("expected ready, got %s", status.State)
	}
}

func TestIsConfigured_IgnoresTokenPlanKeyInQwenSettings(t *testing.T) {
	for _, name := range envVarNames {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	qwenDir := filepath.Join(home, ".qwen")
	if err := os.MkdirAll(qwenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"env": map[string]string{"BAILIAN_TOKEN_PLAN_API_KEY": "sk-sp-token-plan"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qwenDir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if New(config.ProviderConfig{}).IsConfigured() {
		t.Fatal("Token Plan key must not configure the Coding Plan provider")
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(config.ProviderConfig{})
	if p.Name() != "alibaba" {
		t.Errorf("Name() = %q", p.Name())
	}
	if p.DisplayName() != "Alibaba" {
		t.Errorf("DisplayName() = %q", p.DisplayName())
	}
	if !p.SafeForAutoPolling() {
		t.Error("SafeForAutoPolling() should be true for read-only quota metadata")
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name  string
		input any
		zero  bool
	}{
		{"milliseconds", float64(1753700000000), false},
		{"seconds", float64(1753700000), false},
		{"iso8601", "2026-07-28T12:00:00Z", false},
		{"datetime", "2026-07-28 12:00:00", false},
		{"empty string", "", true},
		{"zero", float64(0), true},
		{"nil", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTimestamp(tt.input)
			if tt.zero && !got.IsZero() {
				t.Errorf("expected zero time, got %v", got)
			}
			if !tt.zero && got.IsZero() {
				t.Error("expected non-zero time")
			}
		})
	}
}

func TestExpandJSON(t *testing.T) {
	input := map[string]any{
		"outer": `{"inner": "value"}`,
		"plain": "hello",
		"nested": map[string]any{
			"deep": `[1, 2, 3]`,
		},
	}
	result := expandJSON(input).(map[string]any)

	inner, ok := result["outer"].(map[string]any)
	if !ok {
		t.Fatal("outer should be expanded to map")
	}
	if inner["inner"] != "value" {
		t.Errorf("inner value = %v", inner["inner"])
	}
	if result["plain"] != "hello" {
		t.Errorf("plain should stay string, got %v", result["plain"])
	}
	nested := result["nested"].(map[string]any)
	if _, ok := nested["deep"].([]any); !ok {
		t.Error("deep should be expanded to slice")
	}
}

func TestGeneratedURL(t *testing.T) {
	endpoint := fmt.Sprintf(
		"%s/data/api.json?action=%s&product=broadscope-bailian&api=%s&currentRegionId=%s",
		intlHost, quotaAction, quotaAPIName, intlRegionID,
	)
	want := "https://modelstudio.console.alibabacloud.com/data/api.json" +
		"?action=zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2" +
		"&product=broadscope-bailian" +
		"&api=queryCodingPlanInstanceInfoV2" +
		"&currentRegionId=ap-southeast-1"
	if endpoint != want {
		t.Errorf("generated URL:\n  got  %s\n  want %s", endpoint, want)
	}
}

func TestRedirectStripsCredentials(t *testing.T) {
	var redirectHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHeaders = r.Header.Clone()
		json.NewEncoder(w).Encode(quotaResponse())
	}))
	defer backend.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, backend.URL, http.StatusFound)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-secret-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage after redirect: %v", err)
	}
	if data.IsExpired {
		t.Fatal("unexpected IsExpired after redirect")
	}
	for _, header := range []string{"Authorization", "x-api-key", "X-DashScope-API-Key"} {
		if redirectHeaders.Get(header) != "" {
			t.Errorf("credential header %q forwarded across redirect", header)
		}
	}
}

func TestRegionFallbackOn401(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(quotaResponse())
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage with region fallback: %v", err)
	}
	if data.IsExpired {
		t.Fatal("expected successful fallback, got IsExpired")
	}
	if requests != 2 {
		t.Errorf("expected 2 requests (intl + cn fallback), got %d", requests)
	}
	if len(data.Windows) != 3 {
		t.Fatalf("expected 3 windows from fallback, got %d", len(data.Windows))
	}
}

func TestExpiredInstanceQuotaNotUsed(t *testing.T) {
	resp := map[string]any{
		"codingPlanInstanceInfos": []any{
			map[string]any{
				"status": "EXPIRED",
				"codingPlanQuotaInfo": map[string]any{
					"per5HourUsedQuota":  float64(999),
					"per5HourTotalQuota": float64(999),
				},
			},
			map[string]any{
				"status":   "VALID",
				"isActive": true,
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	p.cfg.APIKey = "sk-sp-test-key"

	data, err := p.FetchUsage(context.Background())
	// The active instance has no quota, so the provider must report no data
	// rather than falling back to the expired instance's quota.
	if err == nil {
		for _, w := range data.Windows {
			if w.Used == 999 {
				t.Fatal("quota from expired instance must not be used")
			}
		}
	}
	// An error ("no coding plan quota data found") is the correct outcome:
	// the selected active instance lacks quota and we refuse to search siblings.
}
